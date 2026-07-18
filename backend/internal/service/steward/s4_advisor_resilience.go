package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"mongojson/backend/internal/domain"
)

const (
	defaultAdvisorFailureThreshold = 3
	defaultAdvisorFailureCooldown  = time.Minute
)

type resilientAutonomyAdvisor struct {
	base             AutonomyAdvisor
	failureThreshold int
	cooldown         time.Duration
	now              func() time.Time

	mu                  sync.Mutex
	consecutiveFailures int
	circuitOpenUntil    time.Time
	lastError           string
}

func supportsNativeAgentTurns(advisor AutonomyAdvisor) bool {
	for {
		resilient, ok := advisor.(*resilientAutonomyAdvisor)
		if !ok || resilient == nil {
			break
		}
		advisor = resilient.base
	}
	_, ok := advisor.(AgentTurnAdvisor)
	return ok
}

func resilientAutonomyAdvisorFromEnv(base AutonomyAdvisor) AutonomyAdvisor {
	if base == nil {
		return DisabledAutonomyAdvisor("disabled")
	}
	if !base.Status().Enabled {
		return base
	}
	threshold := intEnv("STEWARD_LLM_FAILURE_THRESHOLD", defaultAdvisorFailureThreshold)
	if threshold <= 0 || threshold > 100 {
		threshold = defaultAdvisorFailureThreshold
	}
	cooldown := durationEnv("STEWARD_LLM_FAILURE_COOLDOWN", defaultAdvisorFailureCooldown)
	if cooldown <= 0 || cooldown > time.Hour {
		cooldown = defaultAdvisorFailureCooldown
	}
	return &resilientAutonomyAdvisor{
		base:             base,
		failureThreshold: threshold,
		cooldown:         cooldown,
		now:              time.Now,
	}
}

func (a *resilientAutonomyAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	if a == nil || a.base == nil {
		return DisabledAutonomyAdvisor("disabled").Status()
	}
	status := a.base.Status()
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.nowTime()
	if !a.circuitOpenUntil.IsZero() && now.Before(a.circuitOpenUntil) {
		status.CircuitOpen = true
		retryAt := a.circuitOpenUntil.UTC()
		status.RetryAt = &retryAt
		if strings.TrimSpace(status.Reason) == "" {
			status.Reason = fmt.Sprintf("advisor circuit open after %d consecutive failures", a.consecutiveFailures)
		}
	}
	status.ConsecutiveFailures = a.consecutiveFailures
	status.LastError = a.lastError
	return status
}

func (a *resilientAutonomyAdvisor) Suggest(ctx context.Context, input AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error) {
	if a == nil || a.base == nil {
		return AutonomyAdvisorSuggestion{}, fmt.Errorf("autonomy advisor disabled: disabled")
	}
	if err := a.checkCircuit(); err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	suggestion, err := a.base.Suggest(ctx, input)
	if err != nil {
		if errors.Is(err, ErrAdvisorDataLevelDenied) {
			return AutonomyAdvisorSuggestion{}, err
		}
		a.recordFailure(err)
		return AutonomyAdvisorSuggestion{}, err
	}
	a.recordSuccess()
	return suggestion, nil
}

func (a *resilientAutonomyAdvisor) Converse(ctx context.Context, input ConversationAdvisorInput) (ConversationAdvisorResponse, error) {
	if a == nil || a.base == nil {
		return ConversationAdvisorResponse{}, fmt.Errorf("autonomy advisor disabled: disabled")
	}
	conversationAdvisor, ok := a.base.(ConversationAdvisor)
	if !ok {
		return ConversationAdvisorResponse{}, fmt.Errorf("configured advisor does not support conversation")
	}
	if err := a.checkCircuit(); err != nil {
		return ConversationAdvisorResponse{}, err
	}
	response, err := conversationAdvisor.Converse(ctx, input)
	if err != nil {
		if errors.Is(err, ErrAdvisorDataLevelDenied) {
			return ConversationAdvisorResponse{}, err
		}
		a.recordFailure(err)
		return ConversationAdvisorResponse{}, err
	}
	a.recordSuccess()
	return response, nil
}

func (a *resilientAutonomyAdvisor) NextTurn(ctx context.Context, input AgentTurnInput) (AgentTurnDecision, error) {
	if a == nil || a.base == nil {
		return AgentTurnDecision{}, fmt.Errorf("autonomy advisor disabled: disabled")
	}
	advisor, ok := a.base.(AgentTurnAdvisor)
	if !ok {
		legacy, legacyOK := a.base.(ConversationAdvisor)
		if !legacyOK || len(input.Transcript) > 0 {
			return AgentTurnDecision{}, fmt.Errorf("configured advisor does not support agent turns")
		}
		response, err := legacy.Converse(ctx, ConversationAdvisorInput{
			Message: input.Message, DataLevel: input.DataLevel, History: input.History, Context: input.Context,
			Tools: input.Tools, Devices: input.Devices, KnownFolders: input.KnownFolders, CurrentTime: input.CurrentTime,
		})
		if err != nil {
			return AgentTurnDecision{}, err
		}
		decision := AgentTurnDecision{Content: response.Reply}
		if response.ExecutionPlan != nil {
			decision.Content = strings.TrimSpace(strings.Join([]string{response.Reply, response.ExecutionPlan.Summary}, " "))
			decision.ReasoningContent = response.ExecutionPlan.ReasoningContent
			for index, step := range response.ExecutionPlan.Steps {
				decision.ToolCalls = append(decision.ToolCalls, domain.StewardAgentToolCall{ID: fmt.Sprintf("call_%d", index+1), ToolName: step.ToolName, Arguments: step.Arguments, TargetDeviceID: response.TargetDevice})
			}
		}
		return decision, nil
	}
	if err := a.checkCircuit(); err != nil {
		return AgentTurnDecision{}, err
	}
	response, err := advisor.NextTurn(ctx, input)
	if err != nil {
		if errors.Is(err, ErrAdvisorDataLevelDenied) {
			return AgentTurnDecision{}, err
		}
		a.recordFailure(err)
		return AgentTurnDecision{}, err
	}
	a.recordSuccess()
	return response, nil
}

func (a *resilientAutonomyAdvisor) ConcludeToolCalls(ctx context.Context, input ConversationToolResultInput) (string, error) {
	if a == nil || a.base == nil {
		return "", fmt.Errorf("autonomy advisor disabled: disabled")
	}
	advisor, ok := a.base.(ConversationToolResultAdvisor)
	if !ok {
		return "", fmt.Errorf("configured advisor does not support tool result conclusions")
	}
	if err := a.checkCircuit(); err != nil {
		return "", err
	}
	response, err := advisor.ConcludeToolCalls(ctx, input)
	if err != nil {
		if errors.Is(err, ErrAdvisorDataLevelDenied) {
			return "", err
		}
		a.recordFailure(err)
		return "", err
	}
	a.recordSuccess()
	return response, nil
}

func (a *resilientAutonomyAdvisor) AnalyzeObservation(ctx context.Context, input ObservationModelInput) (ObservationModelOutput, error) {
	if a == nil || a.base == nil {
		return ObservationModelOutput{}, fmt.Errorf("autonomy advisor disabled: disabled")
	}
	advisor, ok := a.base.(ObservationModelAdvisor)
	if !ok {
		return ObservationModelOutput{}, fmt.Errorf("configured advisor does not support observation analysis")
	}
	if err := a.checkCircuit(); err != nil {
		return ObservationModelOutput{}, err
	}
	response, err := advisor.AnalyzeObservation(ctx, input)
	if err != nil {
		if errors.Is(err, ErrAdvisorDataLevelDenied) {
			return ObservationModelOutput{}, err
		}
		a.recordFailure(err)
		return ObservationModelOutput{}, err
	}
	a.recordSuccess()
	return response, nil
}

func (a *resilientAutonomyAdvisor) checkCircuit() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.nowTime()
	if a.circuitOpenUntil.IsZero() {
		return nil
	}
	if !now.Before(a.circuitOpenUntil) {
		a.circuitOpenUntil = time.Time{}
		return nil
	}
	return fmt.Errorf("autonomy advisor circuit open until %s", a.circuitOpenUntil.UTC().Format(time.RFC3339))
}

func (a *resilientAutonomyAdvisor) recordFailure(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.consecutiveFailures++
	a.lastError = sanitizeAdvisorStatusError(err)
	if a.consecutiveFailures >= a.failureThreshold {
		a.circuitOpenUntil = a.nowTime().Add(a.cooldown)
	}
}

func (a *resilientAutonomyAdvisor) recordSuccess() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.consecutiveFailures = 0
	a.circuitOpenUntil = time.Time{}
	a.lastError = ""
}

func (a *resilientAutonomyAdvisor) nowTime() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func sanitizeAdvisorStatusError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if value == "" {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return value
}
