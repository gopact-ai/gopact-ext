package react

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	defaultDeferredMemoryWorkRetryMaxAttempts   = 3
	defaultDeferredMemoryWorkRetryInitialDelay  = 100 * time.Millisecond
	defaultDeferredMemoryWorkRetryMaxDelay      = 30 * time.Second
	defaultDeferredMemoryWorkRetryBackoffFactor = 2
)

var ErrDeferredMemoryWorkRetryPolicyInvalid = errors.New("react: deferred memory work retry policy is invalid")

// DeferredMemoryWorkRetryPolicy configures the default retry/backoff decider.
//
// Zero values are usable: max attempts defaults to three, initial delay defaults
// to 100ms, max delay defaults to 30s, and backoff factor defaults to two.
// The decider only returns schedule decisions; queueing, sleeping, concurrency,
// leasing, and durable DLQ storage remain host or adapter responsibilities.
type DeferredMemoryWorkRetryPolicy struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor int
	Reason        string
	Metadata      map[string]any
}

// DeferredMemoryWorkRetryDecider retries failed memory work with capped exponential backoff.
type DeferredMemoryWorkRetryDecider struct {
	policy DeferredMemoryWorkRetryPolicy
}

var _ DeferredMemoryWorkScheduleDecider = (*DeferredMemoryWorkRetryDecider)(nil)

// NewDeferredMemoryWorkRetryDecider creates a default retry/backoff decider.
func NewDeferredMemoryWorkRetryDecider(policy DeferredMemoryWorkRetryPolicy) (*DeferredMemoryWorkRetryDecider, error) {
	if err := validateDeferredMemoryWorkRetryPolicy(policy); err != nil {
		return nil, err
	}
	policy.Metadata = copyAnyMap(policy.Metadata)
	return &DeferredMemoryWorkRetryDecider{policy: policy}, nil
}

// DecideDeferredMemoryWorkSchedule implements DeferredMemoryWorkScheduleDecider.
func (d *DeferredMemoryWorkRetryDecider) DecideDeferredMemoryWorkSchedule(ctx context.Context, request DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error) {
	if d == nil {
		return DeferredMemoryWorkScheduleDecision{}, ErrDeferredMemoryWorkScheduleDeciderRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	if err := ctx.Err(); err != nil {
		return DeferredMemoryWorkScheduleDecision{}, err
	}

	request = copyDeferredMemoryWorkScheduleRequest(request)
	attempt := deferredMemoryWorkRetryAttempt(request)
	if attempt <= 0 {
		return DeferredMemoryWorkScheduleDecision{}, ErrDeferredMemoryWorkScheduleAttemptRequired
	}

	maxAttempts := d.maxAttempts(request)
	decision := DeferredMemoryWorkScheduleDecision{
		Report:      copyDeferredMemoryWorkReport(request.Report),
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		Metadata:    d.decisionMetadata(request.Metadata),
	}
	if attempt >= maxAttempts {
		decision.Action = DeferredMemoryWorkScheduleDeadLetter
		decision.Reason = d.reason("max attempts reached")
		return decision, nil
	}

	decision.Action = DeferredMemoryWorkScheduleRetry
	decision.NextAttempt = attempt + 1
	decision.Delay = d.delay(attempt)
	decision.Reason = d.reason("deferred memory work retry scheduled")
	return decision, nil
}

func validateDeferredMemoryWorkRetryPolicy(policy DeferredMemoryWorkRetryPolicy) error {
	switch {
	case policy.MaxAttempts < 0:
		return fmt.Errorf("react: max attempts must be non-negative: %w", ErrDeferredMemoryWorkRetryPolicyInvalid)
	case policy.InitialDelay < 0:
		return fmt.Errorf("react: initial delay must be non-negative: %w", ErrDeferredMemoryWorkRetryPolicyInvalid)
	case policy.MaxDelay < 0:
		return fmt.Errorf("react: max delay must be non-negative: %w", ErrDeferredMemoryWorkRetryPolicyInvalid)
	case policy.BackoffFactor < 0:
		return fmt.Errorf("react: backoff factor must be non-negative: %w", ErrDeferredMemoryWorkRetryPolicyInvalid)
	default:
		return nil
	}
}

func deferredMemoryWorkRetryAttempt(request DeferredMemoryWorkScheduleRequest) int {
	if request.Attempt > 0 {
		return request.Attempt
	}
	return request.Job.Attempt
}

func (d *DeferredMemoryWorkRetryDecider) maxAttempts(request DeferredMemoryWorkScheduleRequest) int {
	if d.policy.MaxAttempts > 0 {
		return d.policy.MaxAttempts
	}
	if request.MaxAttempts > 0 {
		return request.MaxAttempts
	}
	if request.Job.MaxAttempts > 0 {
		return request.Job.MaxAttempts
	}
	return defaultDeferredMemoryWorkRetryMaxAttempts
}

func (d *DeferredMemoryWorkRetryDecider) delay(attempt int) time.Duration {
	delay := d.initialDelay()
	factor := d.backoffFactor()
	maxDelay := d.maxDelay()
	for i := 1; i < attempt; i++ {
		if factor <= 1 {
			return capDeferredMemoryWorkRetryDelay(delay, maxDelay)
		}
		if delay >= maxDelay {
			return maxDelay
		}
		if delay > maxDelay/time.Duration(factor) {
			return maxDelay
		}
		delay *= time.Duration(factor)
	}
	return capDeferredMemoryWorkRetryDelay(delay, maxDelay)
}

func (d *DeferredMemoryWorkRetryDecider) initialDelay() time.Duration {
	if d.policy.InitialDelay > 0 {
		return d.policy.InitialDelay
	}
	return defaultDeferredMemoryWorkRetryInitialDelay
}

func (d *DeferredMemoryWorkRetryDecider) maxDelay() time.Duration {
	if d.policy.MaxDelay > 0 {
		return d.policy.MaxDelay
	}
	return defaultDeferredMemoryWorkRetryMaxDelay
}

func (d *DeferredMemoryWorkRetryDecider) backoffFactor() int {
	if d.policy.BackoffFactor > 0 {
		return d.policy.BackoffFactor
	}
	return defaultDeferredMemoryWorkRetryBackoffFactor
}

func (d *DeferredMemoryWorkRetryDecider) reason(fallback string) string {
	if d.policy.Reason != "" {
		return d.policy.Reason
	}
	return fallback
}

func (d *DeferredMemoryWorkRetryDecider) decisionMetadata(requestMetadata map[string]any) map[string]any {
	metadata := copyAnyMap(requestMetadata)
	for key, value := range d.policy.Metadata {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata[key] = value
	}
	return metadata
}

func capDeferredMemoryWorkRetryDelay(delay, maxDelay time.Duration) time.Duration {
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}
