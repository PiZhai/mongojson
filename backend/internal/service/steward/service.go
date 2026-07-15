package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/buildinfo"
	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

const (
	DefaultAgentID = "local-s1"

	StatusRunning   = "running"
	StatusStopped   = "stopped"
	StatusHidden    = "hidden"
	StatusActive    = "active"
	StatusArchived  = "archived"
	StatusDeleted   = "deleted"
	StatusDraft     = "draft"
	StatusCandidate = "candidate"
	StatusAccepted  = "accepted"
	StatusDismissed = "dismissed"
	StatusMuted     = "muted"
	StatusOpen      = "open"
	StatusDone      = "done"
	StatusCanceled  = "canceled"
	StatusPending   = "pending"
	StatusResolved  = "resolved"
	StatusBlocked   = "blocked"
	StatusExecuted  = "executed"
	StatusApproved  = "approved"

	PermissionA0  = "A0"
	PermissionA1  = "A1"
	PermissionA2  = "A2"
	PermissionA3  = "A3"
	PermissionA4  = "A4"
	PermissionA5  = "A5"
	PermissionA6  = "A6"
	PermissionA7  = "A7"
	PermissionA8  = "A8"
	PermissionA9  = "A9"
	DataD0        = "D0"
	DataD1        = "D1"
	DataD2        = "D2"
	DataD3        = "D3"
	DataD4        = "D4"
	DataD5        = "D5"
	DataD6        = "D6"
	ResultOK      = "success"
	ResultBlocked = "blocked"
	ResultFailed  = "failed"
)

type Service struct {
	db              *database.DB
	agentID         string
	storageDir      string
	advisor         AutonomyAdvisor
	proposalScorer  AutonomyProposalScorer
	proposalSources *autonomyProposalDiscovererRegistry
	actionExecutors *autonomyActionExecutorRegistry
	retryPolicy     autonomyRetryPolicy
	syncEntities    *syncEntityAdapterRegistry
	peerDiscovery   PeerDiscoveryCatalog

	advisorAuditMu           sync.Mutex
	lastAdvisorFallbackAudit time.Time
}

type ServiceOption func(*Service)

func WithAgentID(agentID string) ServiceOption {
	return func(s *Service) {
		s.agentID = strings.TrimSpace(agentID)
	}
}

func WithStorageDir(storageDir string) ServiceOption {
	return func(s *Service) {
		s.storageDir = strings.TrimSpace(storageDir)
	}
}

func WithAutonomyAdvisor(advisor AutonomyAdvisor) ServiceOption {
	return func(s *Service) {
		s.advisor = advisor
	}
}

func WithAutonomyProposalScorer(scorer AutonomyProposalScorer) ServiceOption {
	return func(s *Service) {
		s.proposalScorer = scorer
	}
}

func WithAutonomyProposalDiscoverer(discoverer AutonomyProposalDiscoverer) ServiceOption {
	return func(s *Service) {
		if s.proposalSources == nil {
			s.proposalSources = newAutonomyProposalDiscovererRegistry()
		}
		s.proposalSources.register(discoverer)
	}
}

func WithAutonomyActionExecutor(executor AutonomyActionExecutor) ServiceOption {
	return func(s *Service) {
		if s.actionExecutors == nil {
			s.actionExecutors = newAutonomyActionExecutorRegistry()
		}
		s.actionExecutors.register(executor)
	}
}

func WithSyncEntityAdapter(adapter SyncEntityAdapter) ServiceOption {
	return func(s *Service) {
		if s.syncEntities == nil {
			s.syncEntities = newSyncEntityAdapterRegistry()
		}
		s.syncEntities.register(adapter)
	}
}

func WithPeerDiscovery(discovery PeerDiscoveryCatalog) ServiceOption {
	return func(s *Service) {
		if discovery != nil {
			s.peerDiscovery = discovery
		}
	}
}

type CreateEventInput struct {
	Type            string `json:"type"`
	Title           string `json:"title"`
	Summary         string `json:"summary"`
	Source          string `json:"source"`
	DataLevel       string `json:"data_level"`
	PermissionLevel string `json:"permission_level"`
	UserConfirmed   *bool  `json:"user_confirmed"`
}

type CreateTaskInput struct {
	ID              string     `json:"-"`
	Type            string     `json:"type"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Priority        string     `json:"priority"`
	DueAt           *time.Time `json:"due_at"`
	Source          string     `json:"source"`
	DataLevel       string     `json:"data_level"`
	PermissionLevel string     `json:"permission_level"`
	RiskLevel       string     `json:"risk_level"`
	UserConfirmed   *bool      `json:"user_confirmed"`
}

type UpdateTaskInput struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Status      *string    `json:"status"`
	Priority    *string    `json:"priority"`
	DueAt       *time.Time `json:"due_at"`
}

type UpdateCollectorInput struct {
	Enabled      *bool          `json:"enabled"`
	ScopeSummary *string        `json:"scope_summary"`
	Settings     map[string]any `json:"settings"`
}

func NewService(db *database.DB, options ...ServiceOption) *Service {
	service := &Service{
		db:             db,
		agentID:        envOrDefault("STEWARD_AGENT_ID", DefaultAgentID),
		storageDir:     envOrDefault("STORAGE_DIR", "./data"),
		advisor:        NewAutonomyAdvisorFromEnv(),
		proposalScorer: NewRuleBasedAutonomyProposalScorer(),
		retryPolicy:    autonomyRetryPolicyFromEnv(),
		peerDiscovery:  disabledPeerDiscovery{},
	}
	service.actionExecutors = newAutonomyActionExecutorRegistry(
		newLocalTaskAutonomyExecutor(service, AutonomyActionCreateLocalTask, "创建本地低风险任务", "autonomous"),
		newLocalTaskAutonomyExecutor(service, AutonomyActionCreateFollowUpTask, "从事件创建本地跟进任务", "autonomous_follow_up"),
		newLocalTaskAutonomyExecutor(service, AutonomyActionCreateReviewChecklist, "创建本地复盘检查清单", "autonomous_review"),
		newReminderTaskAutonomyExecutor(service),
		newKnowledgeSummaryAutonomyExecutor(service),
		newReadOnlyDiagnosticsAutonomyExecutor(service),
	)
	service.proposalSources = newAutonomyProposalDiscovererRegistry(
		newAutonomyProposalDiscoverer("event-follow-up", service.createEventFollowUpProposals),
		newAutonomyProposalDiscoverer("stale-task-review", service.createStaleTaskProposals),
		newAutonomyProposalDiscoverer("event-knowledge-summary", service.createEventKnowledgeSummaryProposals),
		newAutonomyProposalDiscoverer("due-task-reminder", service.createDueTaskReminderProposals),
		newAutonomyProposalDiscoverer("sync-conflict-diagnostics", service.createSyncConflictDiagnosticProposals),
	)
	service.syncEntities = newSyncEntityAdapterRegistry(
		newSyncEntityAdapter(EntityTask, "sync.tasks", PermissionA3, service.applyTaskSyncChange),
		newSyncEntityAdapter(EntityEvent, "sync.timeline", PermissionA3, service.applyEventSyncChange),
		newSyncEntityAdapter(EntityIntent, "sync.tasks", PermissionA3, service.applyIntentSyncChange),
		newSyncEntityAdapter(EntityMemory, "sync.memory", PermissionA3, service.applyMemorySyncChange),
		newSyncEntityAdapter(EntityKnowledgeItem, "sync.knowledge", PermissionA3, service.applyKnowledgeItemSyncChange),
		newSyncEntityAdapter(EntitySourceRef, "sync.knowledge", PermissionA3, service.applySourceRefSyncChange),
		newSyncEntityAdapter(EntityDataTag, "sync.tags", PermissionA3, service.applyDataTagSyncChange),
		newSyncEntityAdapter(EntityEntityTag, "sync.tags", PermissionA3, service.applyEntityTagSyncChange),
		newSyncEntityAdapter(EntityTimeline, "sync.timeline", PermissionA3, service.applyTimelineSegmentSyncChange),
		newSyncEntityAdapter(EntityAuditSummary, "sync.audit", PermissionA3, service.applyAuditSummarySyncChange),
		newSyncEntityAdapter(EntityDeviceRevoke, "sync.devices", PermissionA3, service.applyDeviceRevocationSyncChange),
		newSyncEntityAdapter(EntityDeviceCapability, "sync.devices", PermissionA1, service.applyDeviceCapabilitySyncChange),
	)
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if service.advisor == nil {
		service.advisor = DisabledAutonomyAdvisor("disabled")
	}
	service.advisor = resilientAutonomyAdvisorFromEnv(service.advisor)
	if service.proposalScorer == nil {
		service.proposalScorer = NewRuleBasedAutonomyProposalScorer()
	}
	if service.actionExecutors == nil {
		service.actionExecutors = newAutonomyActionExecutorRegistry()
	}
	if service.proposalSources == nil {
		service.proposalSources = newAutonomyProposalDiscovererRegistry()
	}
	if service.syncEntities == nil {
		service.syncEntities = newSyncEntityAdapterRegistry()
	}
	if service.peerDiscovery == nil {
		service.peerDiscovery = disabledPeerDiscovery{}
	}
	return service
}

func (s *Service) autonomyProposalScorer() AutonomyProposalScorer {
	if s == nil || s.proposalScorer == nil {
		return NewRuleBasedAutonomyProposalScorer()
	}
	return s.proposalScorer
}

func (s *Service) agentIDValue() string {
	if s == nil || strings.TrimSpace(s.agentID) == "" {
		return DefaultAgentID
	}
	return strings.TrimSpace(s.agentID)
}

func (s *Service) autonomyAdvisor() AutonomyAdvisor {
	if s == nil || s.advisor == nil {
		return DisabledAutonomyAdvisor("disabled")
	}
	return s.advisor
}

func (s *Service) EnsureDefaults(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}

	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "local-device"
	}
	now := time.Now().UTC()
	collectorsJSON, _ := json.Marshal([]string{"manual-input"})

	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_agent_status (
			agent_id, device_name, platform, status, version, enabled_collectors, started_at, last_heartbeat_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6::jsonb,$7,$7,$7)
		on conflict (agent_id) do update
		set device_name = excluded.device_name,
		    platform = excluded.platform,
		    version = excluded.version,
		    last_heartbeat_at = excluded.last_heartbeat_at,
		    updated_at = excluded.updated_at
	`, s.agentIDValue(), hostname, runtime.GOOS, StatusRunning, buildinfo.Version, string(collectorsJSON), now); err != nil {
		return fmt.Errorf("ensure steward agent status: %w", err)
	}

	defaults := []domain.StewardCollectorConfig{
		{Name: "manual-input", Enabled: true, ScopeSummary: "用户手动创建事件和任务"},
		{Name: "browser-link", Enabled: false, ScopeSummary: "用户手动导入网页链接"},
		{Name: "clipboard-summary", Enabled: false, ScopeSummary: "剪贴板文本摘要，不保存疑似敏感字段原文"},
		{Name: "watched-directory", Enabled: false, ScopeSummary: "指定目录文件新增、修改、删除元数据"},
		{Name: "system-status", Enabled: false, ScopeSummary: "磁盘、网络和本地 Agent 状态摘要"},
		{Name: "screenpipe-bridge", Enabled: false, ScopeSummary: "固定版本 Screenpipe 本地 API，禁用键盘内容采集"},
		{Name: "activitywatch-bridge", Enabled: false, ScopeSummary: "ActivityWatch bucket/event/heartbeat 导入"},
	}
	for _, collector := range defaults {
		if _, err := s.db.Pool.Exec(ctx, `
			insert into steward_collector_configs (id, name, enabled, scope_summary, created_at, updated_at)
			values ($1,$2,$3,$4,$5,$5)
			on conflict (name) do nothing
		`, uuid.NewString(), collector.Name, collector.Enabled, collector.ScopeSummary, now); err != nil {
			return fmt.Errorf("ensure steward collector %s: %w", collector.Name, err)
		}
	}

	if err := s.ensureS3Defaults(ctx, hostname, runtime.GOOS, now); err != nil {
		return err
	}
	if err := s.ensureS4Defaults(ctx, now); err != nil {
		return err
	}
	if err := s.ensureLocalDeviceCapabilities(ctx, now); err != nil {
		return err
	}
	if err := s.ensureActivityDefaults(ctx, now); err != nil {
		return err
	}
	if err := s.ensureAutomationPolicyDefaults(ctx, now); err != nil {
		return err
	}

	return nil
}

func (s *Service) GetOverview(ctx context.Context) (domain.StewardOverview, error) {
	if err := s.EnsureDefaults(ctx); err != nil {
		return domain.StewardOverview{}, err
	}

	agent, err := s.GetAgentStatus(ctx)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	collectors, err := s.ListCollectors(ctx)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	events, err := s.ListEvents(ctx, 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	timelineSegments, err := s.ListTimelineSegments(ctx, 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	tasks, err := s.ListTasks(ctx, 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	intents, err := s.ListIntents(ctx, 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	memories, err := s.ListMemories(ctx, 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	knowledgeItems, err := s.ListKnowledgeItems(ctx, 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	sourceRefs, err := s.ListSourceRefs(ctx, "", "", 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	tags, err := s.ListDataTags(ctx)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	auditLogs, err := s.ListAuditLogs(ctx, 12)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	counts, err := s.Counts(ctx)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	syncStatus, err := s.GetSyncStatus(ctx)
	if err != nil {
		return domain.StewardOverview{}, err
	}
	autonomy, err := s.GetAutonomyOverview(ctx)
	if err != nil {
		return domain.StewardOverview{}, err
	}

	return domain.StewardOverview{
		Agent:            agent,
		Collectors:       collectors,
		Events:           events,
		TimelineSegments: timelineSegments,
		Tasks:            tasks,
		Intents:          intents,
		Memories:         memories,
		KnowledgeItems:   knowledgeItems,
		SourceRefs:       sourceRefs,
		Tags:             tags,
		AuditLogs:        auditLogs,
		Sync:             &syncStatus,
		Autonomy:         &autonomy,
		Counts:           counts,
	}, nil
}

func (s *Service) GetAgentStatus(ctx context.Context) (domain.StewardAgentStatus, error) {
	var status domain.StewardAgentStatus
	var collectorsJSON string
	if err := s.db.Pool.QueryRow(ctx, `
		select agent_id, device_name, platform, status, version,
		       coalesce(enabled_collectors, '[]'::jsonb)::text,
		       started_at, last_heartbeat_at, last_error, updated_at
		from steward_agent_status
		where agent_id = $1
	`, s.agentIDValue()).Scan(
		&status.AgentID,
		&status.DeviceName,
		&status.Platform,
		&status.Status,
		&status.Version,
		&collectorsJSON,
		&status.StartedAt,
		&status.LastHeartbeatAt,
		&status.LastError,
		&status.UpdatedAt,
	); err != nil {
		return domain.StewardAgentStatus{}, fmt.Errorf("get steward agent status: %w", err)
	}
	_ = json.Unmarshal([]byte(collectorsJSON), &status.EnabledCollectors)
	loops, err := s.listDaemonLoopStatuses(ctx)
	if err != nil {
		return domain.StewardAgentStatus{}, err
	}
	status.BackgroundLoops = loops
	return status, nil
}

func (s *Service) Heartbeat(ctx context.Context, errorSummary string) error {
	now := time.Now().UTC()
	var lastError *string
	if strings.TrimSpace(errorSummary) != "" {
		value := strings.TrimSpace(errorSummary)
		lastError = &value
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_agent_status
		set status = case when status = $1 then status else $2 end,
		    started_at = coalesce(started_at, $3),
		    last_heartbeat_at = $3,
		    last_error = $4,
		    updated_at = $3
		where agent_id = $5
	`, StatusStopped, StatusRunning, now, lastError, s.agentIDValue()); err != nil {
		return fmt.Errorf("record steward heartbeat: %w", err)
	}
	return nil
}

func (s *Service) BackgroundWorkEnabled(ctx context.Context) (bool, error) {
	status, err := s.GetAgentStatus(ctx)
	if err != nil {
		return false, err
	}
	return agentAllowsBackgroundWork(status.Status), nil
}

func agentAllowsBackgroundWork(status string) bool {
	return strings.TrimSpace(status) == StatusRunning
}

func (s *Service) StartAgent(ctx context.Context) (domain.StewardAgentStatus, error) {
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_agent_status
		set status = $1, started_at = coalesce(started_at, $2), last_heartbeat_at = $2, last_error = null, updated_at = $2
		where agent_id = $3
	`, StatusRunning, now, s.agentIDValue()); err != nil {
		return domain.StewardAgentStatus{}, fmt.Errorf("start steward agent: %w", err)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "agent.start",
		TargetType:      "agent",
		TargetID:        nil,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    "start local S1 agent",
		OutputSummary:   "agent marked running",
		ResultStatus:    ResultOK,
	})
	return s.GetAgentStatus(ctx)
}

func (s *Service) StopAgent(ctx context.Context) (domain.StewardAgentStatus, error) {
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_agent_status
		set status = $1, last_heartbeat_at = $2, updated_at = $2
		where agent_id = $3
	`, StatusStopped, now, s.agentIDValue()); err != nil {
		return domain.StewardAgentStatus{}, fmt.Errorf("stop steward agent: %w", err)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "agent.stop",
		TargetType:      "agent",
		TargetID:        nil,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    "stop local S1 agent",
		OutputSummary:   "agent marked stopped",
		ResultStatus:    ResultOK,
	})
	return s.GetAgentStatus(ctx)
}

func (s *Service) ListCollectors(ctx context.Context) ([]domain.StewardCollectorConfig, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, name, enabled, scope_summary, settings, last_run_at, last_error, created_at, updated_at, audit_id
		from steward_collector_configs
		order by name
	`)
	if err != nil {
		return nil, fmt.Errorf("list steward collectors: %w", err)
	}
	defer rows.Close()

	collectors := []domain.StewardCollectorConfig{}
	for rows.Next() {
		var collector domain.StewardCollectorConfig
		if err := rows.Scan(
			&collector.ID,
			&collector.Name,
			&collector.Enabled,
			&collector.ScopeSummary,
			&collector.Settings,
			&collector.LastRunAt,
			&collector.LastError,
			&collector.CreatedAt,
			&collector.UpdatedAt,
			&collector.AuditID,
		); err != nil {
			return nil, err
		}
		collectors = append(collectors, collector)
	}
	return collectors, rows.Err()
}

func (s *Service) UpdateCollector(ctx context.Context, name string, input UpdateCollectorInput) (domain.StewardCollectorConfig, error) {
	now := time.Now().UTC()
	current, err := s.getCollector(ctx, name)
	if err != nil {
		return domain.StewardCollectorConfig{}, err
	}
	enabled := current.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	scopeSummary := current.ScopeSummary
	if input.ScopeSummary != nil {
		scopeSummary = strings.TrimSpace(*input.ScopeSummary)
	}
	settings := current.Settings
	if input.Settings != nil {
		settings, err = normalizeCollectorSettings(name, input.Settings)
		if err != nil {
			return domain.StewardCollectorConfig{}, err
		}
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return domain.StewardCollectorConfig{}, fmt.Errorf("encode steward collector settings: %w", err)
	}

	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "collector.update",
		TargetType:      "collector",
		TargetID:        &current.ID,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    current.Name,
		OutputSummary:   fmt.Sprintf("enabled=%t scope=%s settings_configured=%t", enabled, scopeSummary, len(settings) > 0),
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardCollectorConfig{}, err
	}

	if _, err := s.db.Pool.Exec(ctx, `
		update steward_collector_configs
		set enabled = $1, scope_summary = $2, settings = $3::jsonb, updated_at = $4, audit_id = $5
		where name = $6
	`, enabled, scopeSummary, string(settingsJSON), now, auditID, name); err != nil {
		return domain.StewardCollectorConfig{}, fmt.Errorf("update steward collector: %w", err)
	}
	if err := s.refreshEnabledCollectors(ctx); err != nil {
		return domain.StewardCollectorConfig{}, err
	}
	return s.getCollector(ctx, name)
}

func (s *Service) CreateEvent(ctx context.Context, input CreateEventInput) (domain.StewardEvent, error) {
	now := time.Now().UTC()
	record := domain.StewardEvent{
		ID:              uuid.NewString(),
		Type:            defaultString(input.Type, "manual_note"),
		Title:           defaultString(strings.TrimSpace(input.Title), "手动事件"),
		Summary:         strings.TrimSpace(input.Summary),
		Source:          defaultString(input.Source, "manual"),
		DataLevel:       defaultString(input.DataLevel, DataD0),
		PermissionLevel: defaultString(input.PermissionLevel, PermissionA3),
		Status:          StatusActive,
		DeviceID:        s.agentIDValue(),
		UserConfirmed:   defaultBool(input.UserConfirmed, true),
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	actor := "user"
	if strings.HasPrefix(record.Source, "collector:") {
		actor = "system"
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           actor,
		Action:          "event.create",
		TargetType:      "event",
		TargetID:        &record.ID,
		Source:          record.Source,
		PermissionLevel: PermissionA3,
		DataLevel:       record.DataLevel,
		InputSummary:    record.Title,
		OutputSummary:   "event created",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardEvent{}, err
	}
	record.AuditID = &auditID

	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_events (
			id, type, title, summary, source, data_level, permission_level, status, device_id,
			user_confirmed, version, audit_id, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
	`, record.ID, record.Type, record.Title, record.Summary, record.Source, record.DataLevel,
		record.PermissionLevel, record.Status, record.DeviceID, record.UserConfirmed, record.Version, auditID, now); err != nil {
		return domain.StewardEvent{}, fmt.Errorf("create steward event: %w", err)
	}
	_ = s.recordEventSyncChange(ctx, record, SyncCreate)
	return record, nil
}

func (s *Service) ListEvents(ctx context.Context, limit int) ([]domain.StewardEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, type, title, summary, source, data_level, permission_level, status, device_id,
		       user_confirmed, version, audit_id, created_at, updated_at, deleted_at
		from steward_events
		where deleted_at is null and status <> $1
		order by created_at desc
		limit $2
	`, StatusHidden, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward events: %w", err)
	}
	defer rows.Close()

	events := []domain.StewardEvent{}
	for rows.Next() {
		record, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, record)
	}
	return events, rows.Err()
}

func (s *Service) DeleteEvent(ctx context.Context, id string) error {
	now := time.Now().UTC()
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "event.delete",
		TargetType:      "event",
		TargetID:        &id,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    id,
		OutputSummary:   "event soft deleted",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return err
	}
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_events
		set status = $4, deleted_at = $1, updated_at = $1, audit_id = $2, version = version + 1
		where id = $3 and deleted_at is null
	`, now, auditID, id, StatusDeleted)
	if err != nil {
		return fmt.Errorf("delete steward event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("event not found")
	}
	event, err := s.getEvent(ctx, id)
	if err == nil {
		_ = s.recordEventSyncChange(ctx, event, SyncDelete)
	}
	return nil
}

func (s *Service) HideEvent(ctx context.Context, id string) (domain.StewardEvent, error) {
	now := time.Now().UTC()
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "event.hide",
		TargetType:      "event",
		TargetID:        &id,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    id,
		OutputSummary:   "event hidden",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardEvent{}, err
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_events
		set status = $1, updated_at = $2, audit_id = $3, version = version + 1
		where id = $4 and deleted_at is null
	`, StatusHidden, now, auditID, id); err != nil {
		return domain.StewardEvent{}, fmt.Errorf("hide steward event: %w", err)
	}
	event, err := s.getEvent(ctx, id)
	if err != nil {
		return domain.StewardEvent{}, err
	}
	_ = s.recordEventSyncChange(ctx, event, SyncUpdate)
	return event, nil
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (domain.StewardTask, error) {
	recordID := strings.TrimSpace(input.ID)
	if recordID != "" {
		if _, err := uuid.Parse(recordID); err != nil {
			return domain.StewardTask{}, fmt.Errorf("invalid internal task id: %w", err)
		}
		existing, err := s.getTask(ctx, recordID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardTask{}, err
		}
	} else {
		recordID = uuid.NewString()
	}
	now := time.Now().UTC()
	record := domain.StewardTask{
		ID:              recordID,
		Type:            defaultString(input.Type, "manual"),
		Title:           defaultString(strings.TrimSpace(input.Title), "未命名任务"),
		Description:     strings.TrimSpace(input.Description),
		Status:          StatusOpen,
		Priority:        defaultString(input.Priority, "normal"),
		DueAt:           input.DueAt,
		Source:          defaultString(input.Source, "manual"),
		DataLevel:       defaultString(input.DataLevel, DataD0),
		PermissionLevel: defaultString(input.PermissionLevel, PermissionA3),
		DeviceID:        s.agentIDValue(),
		RiskLevel:       defaultString(input.RiskLevel, "low"),
		UserConfirmed:   defaultBool(input.UserConfirmed, true),
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           auditActorForSource(record.Source),
		Action:          "task.create",
		TargetType:      "task",
		TargetID:        &record.ID,
		Source:          record.Source,
		PermissionLevel: record.PermissionLevel,
		DataLevel:       record.DataLevel,
		InputSummary:    record.Title,
		OutputSummary:   "task created",
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardTask{}, err
	}
	record.AuditID = &auditID

	tag, err := s.db.Pool.Exec(ctx, `
		insert into steward_tasks (
			id, type, title, description, status, priority, due_at, source, data_level, permission_level,
			device_id, risk_level, user_confirmed, version, audit_id, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
		on conflict (id) do nothing
	`, record.ID, record.Type, record.Title, record.Description, record.Status, record.Priority, record.DueAt,
		record.Source, record.DataLevel, record.PermissionLevel, record.DeviceID, record.RiskLevel,
		record.UserConfirmed, record.Version, auditID, now)
	if err != nil {
		return domain.StewardTask{}, fmt.Errorf("create steward task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		existing, err := s.getTask(ctx, record.ID)
		if err != nil {
			return domain.StewardTask{}, fmt.Errorf("load idempotent steward task: %w", err)
		}
		return existing, nil
	}
	_ = s.recordTaskSyncChange(ctx, record, SyncCreate)
	return record, nil
}

func (s *Service) ListTasks(ctx context.Context, limit int) ([]domain.StewardTask, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, type, title, description, status, priority, due_at, source, data_level, permission_level,
		       device_id, risk_level, user_confirmed, version, audit_id, created_at, updated_at, deleted_at, completed_at, canceled_at
		from steward_tasks
		where deleted_at is null and status <> 'deleted'
		order by
			case when status = 'open' then 0 when status = 'in_progress' then 1 else 2 end,
			updated_at desc
		limit $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward tasks: %w", err)
	}
	defer rows.Close()

	tasks := []domain.StewardTask{}
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, record)
	}
	return tasks, rows.Err()
}

func (s *Service) UpdateTask(ctx context.Context, id string, input UpdateTaskInput) (domain.StewardTask, error) {
	current, err := s.getTask(ctx, id)
	if err != nil {
		return domain.StewardTask{}, err
	}
	if input.Title != nil {
		current.Title = defaultString(strings.TrimSpace(*input.Title), current.Title)
	}
	if input.Description != nil {
		current.Description = strings.TrimSpace(*input.Description)
	}
	if input.Priority != nil {
		current.Priority = defaultString(*input.Priority, current.Priority)
	}
	if input.DueAt != nil {
		current.DueAt = input.DueAt
	}
	if input.Status != nil {
		current.Status = normalizeTaskStatus(*input.Status, current.Status)
	}
	return s.saveTask(ctx, current, "task.update")
}

func (s *Service) CompleteTask(ctx context.Context, id string) (domain.StewardTask, error) {
	record, err := s.getTask(ctx, id)
	if err != nil {
		return domain.StewardTask{}, err
	}
	now := time.Now().UTC()
	record.Status = StatusDone
	record.CompletedAt = &now
	return s.saveTask(ctx, record, "task.complete")
}

func (s *Service) CancelTask(ctx context.Context, id string) (domain.StewardTask, error) {
	record, err := s.getTask(ctx, id)
	if err != nil {
		return domain.StewardTask{}, err
	}
	now := time.Now().UTC()
	record.Status = StatusCanceled
	record.CanceledAt = &now
	return s.saveTask(ctx, record, "task.cancel")
}

func (s *Service) ListAuditLogs(ctx context.Context, limit int) ([]domain.StewardAuditLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, occurred_at, actor, action, target_type, target_id, source, permission_level, data_level,
		       input_summary, output_summary, before_summary, after_summary, reason, user_confirmed, syncable,
		       version, device_id, result_status, error_summary
		from steward_audit_logs
		order by occurred_at desc
		limit $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward audit logs: %w", err)
	}
	defer rows.Close()

	logs := []domain.StewardAuditLog{}
	for rows.Next() {
		record, err := scanAuditLog(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, record)
	}
	return logs, rows.Err()
}

func (s *Service) Counts(ctx context.Context) (map[string]int, error) {
	counts := map[string]int{}
	queries := map[string]string{
		"events":            `select count(*) from steward_events where deleted_at is null`,
		"timeline_segments": `select count(*) from steward_timeline_segments where deleted_at is null`,
		"tasks":             `select count(*) from steward_tasks where deleted_at is null`,
		"open_tasks":        `select count(*) from steward_tasks where deleted_at is null and status in ('open','in_progress','waiting')`,
		"intents":           `select count(*) from steward_intents where deleted_at is null`,
		"candidate_intents": `select count(*) from steward_intents where deleted_at is null and status = 'candidate'`,
		"memories":          `select count(*) from steward_memories where deleted_at is null`,
		"knowledge_items":   `select count(*) from steward_knowledge_items where deleted_at is null`,
		"source_refs":       `select count(*) from steward_source_refs`,
		"tags":              `select count(*) from steward_data_tags`,
		"audit_logs":        `select count(*) from steward_audit_logs`,
		"observations":      `select count(*) from steward_observations`,
		"activity_sessions": `select count(*) from steward_activity_sessions`,
		"entities":          `select count(*) from steward_entities where status <> 'deleted'`,
		"relations":         `select count(*) from steward_relations where status <> 'deleted'`,
		"habits":            `select count(*) from steward_habits where status <> 'deleted'`,
		"insights":          `select count(*) from steward_insights where status <> 'deleted'`,
	}
	for key, query := range queries {
		var count int
		if err := s.db.Pool.QueryRow(ctx, query).Scan(&count); err != nil {
			return nil, fmt.Errorf("count steward %s: %w", key, err)
		}
		counts[key] = count
	}
	return counts, nil
}

type AuditInput struct {
	Actor           string
	Action          string
	TargetType      string
	TargetID        *string
	Source          string
	PermissionLevel string
	DataLevel       string
	InputSummary    string
	OutputSummary   string
	BeforeSummary   string
	AfterSummary    string
	Reason          string
	UserConfirmed   *bool
	Syncable        *bool
	ResultStatus    string
	ErrorSummary    *string
}

func (s *Service) recordAudit(ctx context.Context, input AuditInput) (string, error) {
	id := uuid.NewString()
	now := time.Now().UTC()
	actor := defaultString(input.Actor, "system")
	source := defaultString(input.Source, "system")
	userConfirmed := defaultBool(input.UserConfirmed, true)
	syncable := defaultBool(input.Syncable, true)
	version := 1
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_audit_logs (
			id, occurred_at, actor, action, target_type, target_id, source, permission_level, data_level,
			input_summary, output_summary, before_summary, after_summary, reason, user_confirmed, syncable,
			version, device_id, result_status, error_summary
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	`, id, now, actor, input.Action, input.TargetType, input.TargetID,
		source, input.PermissionLevel, input.DataLevel,
		input.InputSummary, input.OutputSummary, input.BeforeSummary, input.AfterSummary, input.Reason,
		userConfirmed, syncable, version, s.agentIDValue(),
		input.ResultStatus, input.ErrorSummary); err != nil {
		return "", fmt.Errorf("record steward audit: %w", err)
	}
	_ = s.recordAuditSummarySyncChange(ctx, domain.StewardAuditLog{
		ID: id, OccurredAt: now, Actor: actor, Action: input.Action, TargetType: input.TargetType, TargetID: input.TargetID,
		Source: source, PermissionLevel: input.PermissionLevel, DataLevel: input.DataLevel, OutputSummary: input.OutputSummary,
		Reason: input.Reason, UserConfirmed: userConfirmed, Syncable: syncable, Version: version, DeviceID: s.agentIDValue(),
		ResultStatus: input.ResultStatus, ErrorSummary: input.ErrorSummary,
	})
	return id, nil
}

func (s *Service) getCollector(ctx context.Context, name string) (domain.StewardCollectorConfig, error) {
	var collector domain.StewardCollectorConfig
	if err := s.db.Pool.QueryRow(ctx, `
		select id, name, enabled, scope_summary, settings, last_run_at, last_error, created_at, updated_at, audit_id
		from steward_collector_configs
		where name = $1
	`, name).Scan(
		&collector.ID,
		&collector.Name,
		&collector.Enabled,
		&collector.ScopeSummary,
		&collector.Settings,
		&collector.LastRunAt,
		&collector.LastError,
		&collector.CreatedAt,
		&collector.UpdatedAt,
		&collector.AuditID,
	); err != nil {
		return domain.StewardCollectorConfig{}, fmt.Errorf("get steward collector: %w", err)
	}
	return collector, nil
}

func (s *Service) refreshEnabledCollectors(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `select name from steward_collector_configs where enabled = true order by name`)
	if err != nil {
		return fmt.Errorf("refresh enabled collectors: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	encoded, _ := json.Marshal(names)
	_, err = s.db.Pool.Exec(ctx, `
		update steward_agent_status
		set enabled_collectors = $1::jsonb, updated_at = $2
		where agent_id = $3
	`, string(encoded), time.Now().UTC(), s.agentIDValue())
	if err != nil {
		return fmt.Errorf("update enabled collectors: %w", err)
	}
	return nil
}

func (s *Service) getEvent(ctx context.Context, id string) (domain.StewardEvent, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, type, title, summary, source, data_level, permission_level, status, device_id,
		       user_confirmed, version, audit_id, created_at, updated_at, deleted_at
		from steward_events
		where id = $1
	`, id)
	record, err := scanEvent(row)
	if err != nil {
		return domain.StewardEvent{}, fmt.Errorf("get steward event: %w", err)
	}
	return record, nil
}

func (s *Service) getTask(ctx context.Context, id string) (domain.StewardTask, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, type, title, description, status, priority, due_at, source, data_level, permission_level,
		       device_id, risk_level, user_confirmed, version, audit_id, created_at, updated_at, deleted_at, completed_at, canceled_at
		from steward_tasks
		where id = $1
	`, id)
	record, err := scanTask(row)
	if err != nil {
		return domain.StewardTask{}, fmt.Errorf("get steward task: %w", err)
	}
	return record, nil
}

func (s *Service) saveTask(ctx context.Context, record domain.StewardTask, action string) (domain.StewardTask, error) {
	now := time.Now().UTC()
	record.UpdatedAt = now
	auditID, err := s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          action,
		TargetType:      "task",
		TargetID:        &record.ID,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		InputSummary:    record.Title,
		OutputSummary:   record.Status,
		ResultStatus:    ResultOK,
	})
	if err != nil {
		return domain.StewardTask{}, err
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_tasks
		set title = $1, description = $2, status = $3, priority = $4, due_at = $5,
		    audit_id = $6, updated_at = $7, completed_at = $8, canceled_at = $9,
		    version = version + 1
		where id = $10 and deleted_at is null
	`, record.Title, record.Description, record.Status, record.Priority, record.DueAt,
		auditID, record.UpdatedAt, record.CompletedAt, record.CanceledAt, record.ID); err != nil {
		return domain.StewardTask{}, fmt.Errorf("save steward task: %w", err)
	}
	updated, err := s.getTask(ctx, record.ID)
	if err != nil {
		return domain.StewardTask{}, err
	}
	_ = s.recordTaskSyncChange(ctx, updated, SyncUpdate)
	return updated, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEvent(row scanner) (domain.StewardEvent, error) {
	var record domain.StewardEvent
	err := row.Scan(
		&record.ID,
		&record.Type,
		&record.Title,
		&record.Summary,
		&record.Source,
		&record.DataLevel,
		&record.PermissionLevel,
		&record.Status,
		&record.DeviceID,
		&record.UserConfirmed,
		&record.Version,
		&record.AuditID,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.DeletedAt,
	)
	return record, err
}

func scanTask(row scanner) (domain.StewardTask, error) {
	var record domain.StewardTask
	err := row.Scan(
		&record.ID,
		&record.Type,
		&record.Title,
		&record.Description,
		&record.Status,
		&record.Priority,
		&record.DueAt,
		&record.Source,
		&record.DataLevel,
		&record.PermissionLevel,
		&record.DeviceID,
		&record.RiskLevel,
		&record.UserConfirmed,
		&record.Version,
		&record.AuditID,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.DeletedAt,
		&record.CompletedAt,
		&record.CanceledAt,
	)
	return record, err
}

func scanAuditLog(row scanner) (domain.StewardAuditLog, error) {
	var record domain.StewardAuditLog
	err := row.Scan(
		&record.ID,
		&record.OccurredAt,
		&record.Actor,
		&record.Action,
		&record.TargetType,
		&record.TargetID,
		&record.Source,
		&record.PermissionLevel,
		&record.DataLevel,
		&record.InputSummary,
		&record.OutputSummary,
		&record.BeforeSummary,
		&record.AfterSummary,
		&record.Reason,
		&record.UserConfirmed,
		&record.Syncable,
		&record.Version,
		&record.DeviceID,
		&record.ResultStatus,
		&record.ErrorSummary,
	)
	return record, err
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func auditActorForSource(source string) string {
	switch strings.TrimSpace(source) {
	case "autonomy":
		return "autonomy"
	case "sync":
		return "sync"
	default:
		return "user"
	}
}

func defaultBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func boolEnv(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func normalizeTaskStatus(value string, fallback string) string {
	switch strings.TrimSpace(value) {
	case StatusOpen, "in_progress", "waiting", StatusDone, StatusCanceled, "archived":
		return strings.TrimSpace(value)
	default:
		return fallback
	}
}
