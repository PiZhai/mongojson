package mongoreview

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type reviewState struct {
	review      Review
	sequence    int64
	events      []ReviewEvent
	subscribers map[chan ReviewEvent]struct{}
}

type reviewManager struct {
	service *Service
	mu      sync.RWMutex
	items   map[string]*reviewState
}

func newReviewManager(service *Service) *reviewManager {
	return &reviewManager{service: service, items: map[string]*reviewState{}}
}

func (m *reviewManager) Start(ctx context.Context, input ReviewRequest) (Review, error) {
	if strings.TrimSpace(input.Source) == "" && input.ScriptID != "" {
		script, err := m.service.GetScript(ctx, input.ScriptID)
		if err != nil {
			return Review{}, err
		}
		input.Source = script.Source
	}
	if len(input.Environments) == 0 {
		return Review{}, fmt.Errorf("%w: select at least one environment", ErrInvalidInput)
	}
	for _, environment := range input.Environments {
		if !ValidEnvironments[environment] {
			return Review{}, fmt.Errorf("%w: unsupported environment %s", ErrInvalidInput, environment)
		}
		if _, _, err := m.service.environmentCredential(ctx, environment); err != nil {
			return Review{}, fmt.Errorf("environment %s: %w", environment, err)
		}
	}
	parsed, err := m.service.Parse(ctx, input.Source)
	if err != nil {
		return Review{}, err
	}
	review := Review{
		ID: uuid.NewString(), ScriptID: input.ScriptID, Status: "queued",
		Parse: parsed, Results: []OperationResult{}, CreatedAt: time.Now().UTC(),
	}
	state := &reviewState{review: review, subscribers: map[chan ReviewEvent]struct{}{}}
	m.mu.Lock()
	m.items[review.ID] = state
	m.publishLocked(state, "queued")
	m.mu.Unlock()
	environmentNames, _ := json.Marshal(input.Environments)
	_, _ = m.service.db.Pool.Exec(ctx, `
		insert into mongodb_review_summaries (id, script_id, status, environment_names, operation_count)
		values ($1, nullif($2, '')::uuid, 'queued', $3, $4)
	`, review.ID, input.ScriptID, environmentNames, len(parsed.Operations))
	go m.run(review.ID, input)
	return review, nil
}

func (m *reviewManager) run(id string, input ReviewRequest) {
	m.update(id, func(review *Review) { review.Status = "running" }, "running")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, environment := range input.Environments {
		uri, databaseName, err := m.service.environmentCredential(ctx, environment)
		if err != nil {
			m.fail(id, err)
			return
		}
		client, err := connectReadOnlyMongo(ctx, uri, databaseName)
		if err != nil {
			// Do not retain the driver error: a malformed URI may cause it to
			// include credentials in the in-memory result, SSE, or summary.
			m.fail(id, fmt.Errorf("%s MongoDB connection failed", environment))
			return
		}
		review, _ := m.Get(id)
		for _, operation := range review.Parse.Operations {
			results := m.reviewOperation(ctx, client, environment, operation, input.RuleIDs)
			for _, result := range results {
				result := result
				m.update(id, func(review *Review) {
					review.Results = append(review.Results, result)
				}, "operation")
			}
		}
		closeMongo(client)
	}
	finished := time.Now().UTC()
	m.update(id, func(review *Review) {
		review.Status = "completed"
		review.FinishedAt = &finished
	}, "completed")
	_, _ = m.service.db.Pool.Exec(context.Background(), `
		update mongodb_review_summaries set status = 'completed', finished_at = now() where id = $1
	`, id)
}

func (m *reviewManager) reviewOperation(
	ctx context.Context,
	client *readOnlyMongo,
	environment string,
	operation ParsedOperation,
	ruleIDs map[string]string,
) []OperationResult {
	if operation.Type == "bulkWrite" {
		var results []OperationResult
		for _, child := range operation.Children {
			childOperation, err := expandBulkChild(operation.Collection, child)
			if err != nil {
				results = append(results, OperationResult{
					OperationID: child.ID, Environment: environment, Status: "error", Message: err.Error(),
				})
				if operation.BulkOrdered {
					break
				}
				continue
			}
			result := m.reviewSingle(ctx, client, environment, childOperation, ruleIDs)
			results = append(results, result)
			if operation.BulkOrdered && result.Status == "error" {
				break
			}
		}
		return results
	}
	return []OperationResult{m.reviewSingle(ctx, client, environment, operation, ruleIDs)}
}

func (m *reviewManager) reviewSingle(
	ctx context.Context,
	client *readOnlyMongo,
	environment string,
	operation ParsedOperation,
	ruleIDs map[string]string,
) OperationResult {
	result := OperationResult{
		OperationID: operation.ID, Environment: environment, Status: "ok",
		Diagnostics: append([]Diagnostic(nil), operation.Diagnostics...),
	}
	if !operation.Queryable {
		result.Status = "uncertain"
		result.Message = "操作包含无法静态确定的内容，未查询数据库。"
		return result
	}
	switch operation.Type {
	case "insertOne":
		return m.reviewInsert(ctx, client, environment, operation, firstArgument(operation), ruleIDs)
	case "insertMany":
		var documents []json.RawMessage
		if err := json.Unmarshal(firstArgument(operation), &documents); err != nil {
			result.Status, result.Message = "error", "insertMany 参数必须是静态文档数组。"
			return result
		}
		result.Message = fmt.Sprintf("已逐条检查 %d 条待插入记录。", len(documents))
		for index, document := range documents {
			item := m.reviewInsert(ctx, client, environment, operation, document, ruleIDs)
			item.OperationID = fmt.Sprintf("%s-%d", operation.ID, index)
			result.MatchCount += item.MatchCount
			result.Documents = append(result.Documents, item.Documents...)
			result.Diagnostics = append(result.Diagnostics, item.Diagnostics...)
			if item.Status != "ok" {
				result.Status = item.Status
			}
		}
		return result
	case "updateOne", "updateMany", "deleteOne", "deleteMany":
		return m.reviewMutation(ctx, client, environment, operation)
	default:
		result.Status = "uncertain"
		result.Message = "不支持的操作类型。"
		return result
	}
}

func (m *reviewManager) reviewInsert(
	ctx context.Context,
	client *readOnlyMongo,
	environment string,
	operation ParsedOperation,
	document json.RawMessage,
	ruleIDs map[string]string,
) OperationResult {
	result := OperationResult{OperationID: operation.ID, Environment: environment, Status: "ok"}
	ruleID := ruleIDs[operation.ID]
	if ruleID == "" {
		ruleID = ruleIDs[operation.Collection]
	}
	if ruleID == "" {
		result.Status = "skipped"
		result.Message = "未选择查询规则，不查询数据库。"
		return result
	}
	rule, err := m.service.rule(ctx, ruleID)
	if err != nil {
		result.Status, result.Message = "error", "查询规则不存在。"
		return result
	}
	if rule.Collection != operation.Collection {
		result.Status, result.Message = "error", "查询规则与脚本集合不匹配。"
		return result
	}
	filter, err := buildRuleFilter(document, rule)
	if err != nil {
		result.Status, result.Message = "uncertain", err.Error()
		return result
	}
	count, matches, truncated, err := client.FindAndCount(ctx, operation.Collection, filter, 2)
	if err != nil {
		result.Status, result.Message = "error", err.Error()
		return result
	}
	result.MatchCount, result.Truncated = count, truncated
	switch {
	case count == 0:
		result.Message = "数据库中没有匹配记录，无需对比。"
	case count > 1:
		result.Status = "non_unique"
		result.Message = "查询规则命中多条记录，无法确定唯一对比对象。"
	default:
		result.Message = "找到唯一记录，已完成全字段 BSON 对比。"
		result.Documents = []DocumentResult{{
			Before:      matches[0],
			Differences: compareDocuments(document, matches[0]),
		}}
	}
	return result
}

func (m *reviewManager) reviewMutation(
	ctx context.Context,
	client *readOnlyMongo,
	environment string,
	operation ParsedOperation,
) OperationResult {
	result := OperationResult{OperationID: operation.ID, Environment: environment, Status: "ok"}
	filter, err := decodeFilter(firstArgument(operation))
	if err != nil {
		result.Status, result.Message = "uncertain", err.Error()
		return result
	}
	limit := int64(MaxDetailDocs)
	count, documents, truncated, err := client.FindAndCount(ctx, operation.Collection, filter, limit)
	if err != nil {
		result.Status, result.Message = "error", err.Error()
		return result
	}
	result.MatchCount, result.Truncated = count, truncated
	if strings.HasPrefix(operation.Type, "delete") {
		result.Message = fmt.Sprintf("数据库中存在 %d 条待删除记录。", count)
		for _, document := range documents {
			result.Documents = append(result.Documents, DocumentResult{Before: document})
		}
		return result
	}
	if len(operation.Arguments) < 2 {
		result.Status, result.Message = "error", "更新语句缺少更新参数。"
		return result
	}
	var optionsBody struct {
		Upsert       bool              `json:"upsert"`
		ArrayFilters []json.RawMessage `json:"arrayFilters"`
	}
	if len(operation.Arguments) > 2 {
		_ = json.Unmarshal(operation.Arguments[2], &optionsBody)
	}
	for _, document := range documents {
		simulation, err := m.service.analyzer.SimulateUpdate(
			ctx, document, operation.Arguments[1], optionsBody.ArrayFilters,
		)
		if err != nil {
			result.Status = "uncertain"
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "simulation-error", Message: err.Error(), Severity: "warning", Source: "analyzer",
			})
			result.Documents = append(result.Documents, DocumentResult{Before: document, UncertainPaths: []string{"$"}})
			continue
		}
		result.Documents = append(result.Documents, DocumentResult{
			Before: simulation.Before, After: simulation.After,
			ModifiedPaths: simulation.ModifiedPaths, UncertainPaths: simulation.UncertainPaths,
			Differences: compareDocuments(simulation.Before, simulation.After),
		})
		result.Diagnostics = append(result.Diagnostics, simulation.Diagnostics...)
		if len(simulation.UncertainPaths) > 0 {
			result.Status = "uncertain"
		}
	}
	if count == 0 && optionsBody.Upsert {
		base, ok := equalityBase(filter)
		if !ok {
			result.Status, result.Message = "uncertain", "upsert 过滤条件无法静态转换为新文档。"
			return result
		}
		baseJSON, _ := bson.MarshalExtJSON(base, true, false)
		simulation, err := m.service.analyzer.SimulateUpdate(ctx, baseJSON, operation.Arguments[1], optionsBody.ArrayFilters)
		if err != nil {
			result.Status, result.Message = "uncertain", err.Error()
			return result
		}
		result.Documents = append(result.Documents, DocumentResult{
			Before: simulation.Before, After: simulation.After,
			ModifiedPaths: simulation.ModifiedPaths, UncertainPaths: append(simulation.UncertainPaths, "_id"),
			Differences: compareDocuments(simulation.Before, simulation.After),
		})
		result.Status = "uncertain"
		result.Message = "未命中记录，已模拟 upsert；服务端生成字段标记为无法确定。"
		return result
	}
	result.Message = fmt.Sprintf("预计影响 %d 条记录，展示 %d 条样本。", count, len(documents))
	return result
}

func equalityBase(filter bson.M) (bson.M, bool) {
	result := bson.M{}
	for key, value := range filter {
		if strings.HasPrefix(key, "$") {
			return nil, false
		}
		switch typed := value.(type) {
		case bson.M:
			if len(typed) != 1 {
				return nil, false
			}
			equal, ok := typed["$eq"]
			if !ok {
				return nil, false
			}
			result[key] = equal
		default:
			result[key] = value
		}
	}
	return result, true
}

func expandBulkChild(collection string, child BulkChild) (ParsedOperation, error) {
	if len(child.Arguments) != 1 {
		return ParsedOperation{}, fmt.Errorf("bulkWrite 子操作参数无效")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(child.Arguments[0], &payload); err != nil {
		return ParsedOperation{}, err
	}
	operation := ParsedOperation{
		ID: child.ID, Type: child.Type, Collection: collection, Queryable: child.Queryable,
		UnresolvedPaths: child.UnresolvedPaths, Diagnostics: child.Diagnostics,
	}
	switch child.Type {
	case "insertOne":
		operation.Arguments = []json.RawMessage{payload["document"]}
	case "updateOne", "updateMany":
		operation.Arguments = []json.RawMessage{payload["filter"], payload["update"], childOptions(payload)}
	case "deleteOne", "deleteMany":
		operation.Arguments = []json.RawMessage{payload["filter"]}
	default:
		return ParsedOperation{}, fmt.Errorf("不支持的 bulkWrite 操作 %s", child.Type)
	}
	for _, argument := range operation.Arguments {
		if len(argument) == 0 {
			return ParsedOperation{}, fmt.Errorf("bulkWrite %s 缺少必要参数", child.Type)
		}
	}
	return operation, nil
}

func childOptions(payload map[string]json.RawMessage) json.RawMessage {
	options := map[string]json.RawMessage{}
	for _, key := range []string{"upsert", "arrayFilters"} {
		if value := payload[key]; len(value) > 0 {
			options[key] = value
		}
	}
	encoded, _ := json.Marshal(options)
	return encoded
}

func firstArgument(operation ParsedOperation) json.RawMessage {
	if len(operation.Arguments) == 0 {
		return nil
	}
	return operation.Arguments[0]
}

func compareDocuments(script, database json.RawMessage) []FieldDifference {
	var left any
	var right any
	if json.Unmarshal(script, &left) != nil || json.Unmarshal(database, &right) != nil {
		return nil
	}
	var differences []FieldDifference
	compareValue("$", left, right, &differences)
	return differences
}

func compareValue(path string, left, right any, differences *[]FieldDifference) {
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap && rightIsMap {
		if isExtendedJSONWrapper(leftMap) || isExtendedJSONWrapper(rightMap) {
			if !reflect.DeepEqual(left, right) {
				*differences = append(*differences, makeDifference(path, left, right, "changed"))
			}
			return
		}
		keys := map[string]bool{}
		for key := range leftMap {
			keys[key] = true
		}
		for key := range rightMap {
			keys[key] = true
		}
		for key := range keys {
			childPath := path + "." + key
			leftValue, leftExists := leftMap[key]
			rightValue, rightExists := rightMap[key]
			switch {
			case !leftExists:
				*differences = append(*differences, makeDifference(childPath, nil, rightValue, "database_only"))
			case !rightExists:
				*differences = append(*differences, makeDifference(childPath, leftValue, nil, "script_only"))
			default:
				compareValue(childPath, leftValue, rightValue, differences)
			}
		}
		return
	}
	if !reflect.DeepEqual(left, right) {
		*differences = append(*differences, makeDifference(path, left, right, "changed"))
	}
}

func isExtendedJSONWrapper(value map[string]any) bool {
	if len(value) == 0 {
		return false
	}
	for key := range value {
		if !strings.HasPrefix(key, "$") {
			return false
		}
	}
	return true
}

func makeDifference(path string, script, database any, kind string) FieldDifference {
	left, _ := json.Marshal(script)
	right, _ := json.Marshal(database)
	return FieldDifference{Path: path, Script: left, Database: right, Kind: kind}
}

func (m *reviewManager) Get(id string) (Review, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.items[id]
	if state == nil {
		return Review{}, ErrNotFound
	}
	return cloneReview(state.review), nil
}

func (m *reviewManager) Subscribe(id string, after int64) (<-chan ReviewEvent, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.items[id]
	if state == nil {
		return nil, nil, ErrNotFound
	}
	channel := make(chan ReviewEvent, len(state.events)+32)
	for _, event := range state.events {
		if event.Sequence > after {
			channel <- event
		}
	}
	state.subscribers[channel] = struct{}{}
	cancel := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := state.subscribers[channel]; ok {
			delete(state.subscribers, channel)
			close(channel)
		}
	}
	return channel, cancel, nil
}

func (m *reviewManager) update(id string, mutate func(*Review), eventType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.items[id]
	if state == nil {
		return
	}
	mutate(&state.review)
	m.publishLocked(state, eventType)
}

func (m *reviewManager) fail(id string, err error) {
	finished := time.Now().UTC()
	m.update(id, func(review *Review) {
		review.Status, review.Error, review.FinishedAt = "failed", err.Error(), &finished
	}, "failed")
	_, _ = m.service.db.Pool.Exec(context.Background(), `
		update mongodb_review_summaries set status = 'failed', error_message = $2, finished_at = now() where id = $1
	`, id, err.Error())
}

func (m *reviewManager) publishLocked(state *reviewState, eventType string) {
	state.sequence++
	event := ReviewEvent{Sequence: state.sequence, Type: eventType, Review: cloneReview(state.review)}
	state.events = append(state.events, event)
	if len(state.events) > 128 {
		state.events = state.events[len(state.events)-128:]
	}
	for subscriber := range state.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func cloneReview(input Review) Review {
	encoded, _ := json.Marshal(input)
	var result Review
	_ = json.Unmarshal(encoded, &result)
	return result
}
