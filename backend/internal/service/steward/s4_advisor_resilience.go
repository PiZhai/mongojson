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
	defaultAdvisorRetryAttempts    = 3
	defaultAdvisorRetryBaseDelay   = 200 * time.Millisecond
)

type resilientAutonomyAdvisor struct {
	base             AutonomyAdvisor
	failureThreshold int
	cooldown         time.Duration
	retryBase        time.Duration
	now              func() time.Time

	mu                  sync.Mutex
	consecutiveFailures int
	circuitOpenUntil    time.Time
	lastError           string
	stateGeneration     uint64
	halfOpenInFlight    bool
}

type advisorCallToken struct {
	generation       uint64
	halfOpen         bool
	forcedProbe      bool
	priorFailures    int
	priorCircuitOpen time.Time
	priorLastError   string
}

// AdvisorCircuitOpenError means that no provider request was attempted.  It
// is deliberately typed so callers can persist a wake-up time instead of
// treating every daemon tick during the cool-down window as another model
// failure.
type AdvisorCircuitOpenError struct {
	RetryAt   time.Time
	LastError string
}

func (e *AdvisorCircuitOpenError) Error() string {
	if e == nil {
		return "autonomy advisor circuit open"
	}
	return fmt.Sprintf("autonomy advisor circuit open until %s", e.RetryAt.UTC().Format(time.RFC3339))
}

func advisorCircuitRetryAt(err error) (time.Time, bool) {
	var circuitErr *AdvisorCircuitOpenError
	if !errors.As(err, &circuitErr) || circuitErr == nil || circuitErr.RetryAt.IsZero() {
		return time.Time{}, false
	}
	return circuitErr.RetryAt.UTC(), true
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
		retryBase:        defaultAdvisorRetryBaseDelay,
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
	token, err := a.beginCall(false)
	if err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	suggestion, err := advisorCallWithRetry(ctx, a, func() (AutonomyAdvisorSuggestion, error) { return a.base.Suggest(ctx, input) })
	a.completeCall(token, err)
	return suggestion, err
}

func (a *resilientAutonomyAdvisor) Converse(ctx context.Context, input ConversationAdvisorInput) (ConversationAdvisorResponse, error) {
	if a == nil || a.base == nil {
		return ConversationAdvisorResponse{}, fmt.Errorf("autonomy advisor disabled: disabled")
	}
	conversationAdvisor, ok := a.base.(ConversationAdvisor)
	if !ok {
		return ConversationAdvisorResponse{}, fmt.Errorf("configured advisor does not support conversation")
	}
	token, err := a.beginCall(false)
	if err != nil {
		return ConversationAdvisorResponse{}, err
	}
	response, err := advisorCallWithRetry(ctx, a, func() (ConversationAdvisorResponse, error) { return conversationAdvisor.Converse(ctx, input) })
	a.completeCall(token, err)
	return response, err
}

func (a *resilientAutonomyAdvisor) NextTurn(ctx context.Context, input AgentTurnInput) (AgentTurnDecision, error) {
	if a == nil || a.base == nil {
		return AgentTurnDecision{}, fmt.Errorf("autonomy advisor disabled: disabled")
	}
	advisor, ok := a.base.(AgentTurnAdvisor)
	var legacy ConversationAdvisor
	if !ok {
		var legacyOK bool
		legacy, legacyOK = a.base.(ConversationAdvisor)
		if !legacyOK || len(input.Transcript) > 0 {
			return AgentTurnDecision{}, fmt.Errorf("configured advisor does not support agent turns")
		}
	}
	token, err := a.beginCall(false)
	if err != nil {
		return AgentTurnDecision{}, err
	}
	if !ok {
		response, err := advisorCallWithRetry(ctx, a, func() (ConversationAdvisorResponse, error) {
			return legacy.Converse(ctx, ConversationAdvisorInput{
				Message: input.Message, DataLevel: input.DataLevel, History: input.History, Context: input.Context,
				Tools: input.Tools, Devices: input.Devices, KnownFolders: input.KnownFolders, CurrentTime: input.CurrentTime,
			})
		})
		a.completeCall(token, err)
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
	response, err := advisorCallWithRetry(ctx, a, func() (AgentTurnDecision, error) { return advisor.NextTurn(ctx, input) })
	a.completeCall(token, err)
	return response, err
}

func (a *resilientAutonomyAdvisor) ConcludeToolCalls(ctx context.Context, input ConversationToolResultInput) (string, error) {
	if a == nil || a.base == nil {
		return "", fmt.Errorf("autonomy advisor disabled: disabled")
	}
	advisor, ok := a.base.(ConversationToolResultAdvisor)
	if !ok {
		return "", fmt.Errorf("configured advisor does not support tool result conclusions")
	}
	token, err := a.beginCall(false)
	if err != nil {
		return "", err
	}
	response, err := advisorCallWithRetry(ctx, a, func() (string, error) { return advisor.ConcludeToolCalls(ctx, input) })
	a.completeCall(token, err)
	return response, err
}

func (a *resilientAutonomyAdvisor) AnalyzeObservation(ctx context.Context, input ObservationModelInput) (ObservationModelOutput, error) {
	if a == nil || a.base == nil {
		return ObservationModelOutput{}, fmt.Errorf("autonomy advisor disabled: disabled")
	}
	advisor, ok := a.base.(ObservationModelAdvisor)
	if !ok {
		return ObservationModelOutput{}, fmt.Errorf("configured advisor does not support observation analysis")
	}
	token, err := a.beginCall(false)
	if err != nil {
		return ObservationModelOutput{}, err
	}
	response, err := advisorCallWithRetry(ctx, a, func() (ObservationModelOutput, error) { return advisor.AnalyzeObservation(ctx, input) })
	a.completeCall(token, err)
	return response, err
}

func (a *resilientAutonomyAdvisor) beginCall(forceProbe bool) (advisorCallToken, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.nowTime()
	open := !a.circuitOpenUntil.IsZero()
	if open && now.Before(a.circuitOpenUntil) && !forceProbe {
		return advisorCallToken{}, &AdvisorCircuitOpenError{RetryAt: a.circuitOpenUntil.UTC(), LastError: a.lastError}
	}
	if (forceProbe || open) && a.halfOpenInFlight {
		retryAt := a.circuitOpenUntil
		if !retryAt.After(now) {
			retryAt = now.Add(time.Second)
		}
		return advisorCallToken{}, &AdvisorCircuitOpenError{RetryAt: retryAt.UTC(), LastError: a.lastError}
	}
	token := advisorCallToken{
		generation:       a.stateGeneration,
		forcedProbe:      forceProbe,
		priorFailures:    a.consecutiveFailures,
		priorCircuitOpen: a.circuitOpenUntil,
		priorLastError:   a.lastError,
	}
	if forceProbe || open {
		a.stateGeneration++
		token.generation = a.stateGeneration
		token.halfOpen = true
		a.halfOpenInFlight = true
	}
	return token, nil
}

// advisorFailureAffectsCircuit limits the shared provider breaker to failures
// that say something about provider health.  Bad local tool names/schemas,
// protocol incompatibilities, authentication/configuration errors and caller
// cancellation must remain visible to their own request, but must not prevent
// unrelated conversations from reaching a healthy provider.
func advisorFailureAffectsCircuit(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrAdvisorDataLevelDenied) {
		return false
	}
	switch describeAdvisorFailure(err).Code {
	case "MODEL_NETWORK_ERROR", "MODEL_TIMEOUT", "MODEL_RATE_LIMITED", "MODEL_PROVIDER_UNAVAILABLE":
		return true
	default:
		return false
	}
}

func (a *resilientAutonomyAdvisor) completeCall(token advisorCallToken, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if token.generation != a.stateGeneration {
		if token.halfOpen {
			a.halfOpenInFlight = false
		}
		return
	}
	if token.halfOpen {
		a.halfOpenInFlight = false
	}
	if err == nil {
		a.consecutiveFailures = 0
		a.circuitOpenUntil = time.Time{}
		a.lastError = ""
		return
	}
	if !advisorFailureAffectsCircuit(err) {
		// A real response carrying a local/protocol/configuration error proves
		// the transport is no longer in the old provider outage. It is reported
		// to the caller but does not poison the shared health breaker.
		if token.forcedProbe {
			a.consecutiveFailures = token.priorFailures
			a.circuitOpenUntil = token.priorCircuitOpen
			a.lastError = token.priorLastError
		} else if token.halfOpen {
			a.circuitOpenUntil = time.Time{}
			a.consecutiveFailures = 0
		}
		return
	}
	a.consecutiveFailures++
	a.lastError = sanitizeAdvisorStatusError(err)
	threshold := a.failureThreshold
	if threshold <= 0 {
		threshold = defaultAdvisorFailureThreshold
	}
	if token.halfOpen || a.consecutiveFailures >= threshold {
		cooldown := a.cooldown
		if cooldown <= 0 {
			cooldown = defaultAdvisorFailureCooldown
		}
		a.circuitOpenUntil = a.nowTime().Add(cooldown)
		a.stateGeneration++
	}
}

func advisorCallWithRetry[T any](ctx context.Context, advisor *resilientAutonomyAdvisor, call func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt < defaultAdvisorRetryAttempts; attempt++ {
		value, err := call()
		if err == nil {
			return value, nil
		}
		lastErr = err
		if !advisorFailureAffectsCircuit(err) || attempt+1 >= defaultAdvisorRetryAttempts || ctx.Err() != nil {
			return zero, err
		}
		base := advisor.retryBase
		if base <= 0 {
			continue
		}
		delay := base * time.Duration(1<<attempt)
		// Deterministic per-attempt jitter avoids synchronized retries while
		// keeping tests stable.
		delay += time.Duration((attempt+1)*37) * time.Millisecond
		var httpErr *advisorHTTPError
		if errors.As(err, &httpErr) && httpErr.RetryAfter > delay {
			delay = httpErr.RetryAfter
		}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return zero, ctx.Err()
			}
			if delay >= remaining {
				return zero, err
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, lastErr
}

// ProbeNextTurn and ProbeSuggest are deliberate single half-open probes. They
// bypass an existing cool-down once, allowing a just-fixed API key/network to
// recover immediately without permitting concurrent production requests to
// stampede the provider.
func (a *resilientAutonomyAdvisor) ProbeNextTurn(ctx context.Context, input AgentTurnInput) (AgentTurnDecision, error) {
	advisor, ok := a.base.(AgentTurnAdvisor)
	if !ok {
		return AgentTurnDecision{}, fmt.Errorf("configured advisor does not support agent turns")
	}
	token, err := a.beginCall(true)
	if err != nil {
		return AgentTurnDecision{}, err
	}
	decision, err := advisorCallWithRetry(ctx, a, func() (AgentTurnDecision, error) { return advisor.NextTurn(ctx, input) })
	a.completeCall(token, err)
	return decision, err
}

func (a *resilientAutonomyAdvisor) ProbeSuggest(ctx context.Context, input AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error) {
	token, err := a.beginCall(true)
	if err != nil {
		return AutonomyAdvisorSuggestion{}, err
	}
	suggestion, err := advisorCallWithRetry(ctx, a, func() (AutonomyAdvisorSuggestion, error) { return a.base.Suggest(ctx, input) })
	a.completeCall(token, err)
	return suggestion, err
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
