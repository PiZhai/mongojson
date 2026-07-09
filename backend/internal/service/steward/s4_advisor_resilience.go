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
