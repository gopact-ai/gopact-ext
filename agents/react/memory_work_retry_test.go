package react

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDeferredMemoryWorkRetryDeciderRetriesWithCappedBackoff(t *testing.T) {
	decider, err := NewDeferredMemoryWorkRetryDecider(DeferredMemoryWorkRetryPolicy{
		MaxAttempts:   4,
		InitialDelay:  10 * time.Millisecond,
		MaxDelay:      25 * time.Millisecond,
		BackoffFactor: 2,
		Reason:        "temporary memory store outage",
		Metadata:      map[string]any{"policy": "local"},
	})
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkRetryDecider() error = %v", err)
	}

	requestMetadata := map[string]any{"queue": "memory-default"}
	decision, err := decider.DecideDeferredMemoryWorkSchedule(context.Background(), DeferredMemoryWorkScheduleRequest{
		Report:      failedDeferredMemoryWorkReport(t),
		Attempt:     3,
		MaxAttempts: 4,
		Metadata:    requestMetadata,
	})
	if err != nil {
		t.Fatalf("DecideDeferredMemoryWorkSchedule() error = %v", err)
	}

	if decision.Action != DeferredMemoryWorkScheduleRetry ||
		decision.Attempt != 3 ||
		decision.NextAttempt != 4 ||
		decision.MaxAttempts != 4 ||
		decision.Delay != 25*time.Millisecond ||
		decision.Reason != "temporary memory store outage" {
		t.Fatalf("decision = %+v, want capped retry decision", decision)
	}
	if decision.Metadata["queue"] != "memory-default" || decision.Metadata["policy"] != "local" {
		t.Fatalf("decision metadata = %+v, want request and policy metadata", decision.Metadata)
	}
	requestMetadata["queue"] = "mutated"
	if decision.Metadata["queue"] != "memory-default" {
		t.Fatalf("decision metadata mutation leaked: %+v", decision.Metadata)
	}
}

func TestDeferredMemoryWorkRetryDeciderDeadLettersAtMaxAttempts(t *testing.T) {
	decider, err := NewDeferredMemoryWorkRetryDecider(DeferredMemoryWorkRetryPolicy{
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkRetryDecider() error = %v", err)
	}

	decision, err := decider.DecideDeferredMemoryWorkSchedule(context.Background(), DeferredMemoryWorkScheduleRequest{
		Report:  failedDeferredMemoryWorkReport(t),
		Attempt: 3,
	})
	if err != nil {
		t.Fatalf("DecideDeferredMemoryWorkSchedule() error = %v", err)
	}

	if decision.Action != DeferredMemoryWorkScheduleDeadLetter ||
		decision.Attempt != 3 ||
		decision.NextAttempt != 0 ||
		decision.MaxAttempts != 3 {
		t.Fatalf("decision = %+v, want dead-letter at max attempts", decision)
	}
	if decision.Reason == "" {
		t.Fatal("decision reason = empty, want max attempts reason")
	}
}

func TestNewDeferredMemoryWorkRetryDeciderRejectsInvalidPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy DeferredMemoryWorkRetryPolicy
	}{
		{
			name:   "negative max attempts",
			policy: DeferredMemoryWorkRetryPolicy{MaxAttempts: -1},
		},
		{
			name:   "negative initial delay",
			policy: DeferredMemoryWorkRetryPolicy{InitialDelay: -1},
		},
		{
			name:   "negative max delay",
			policy: DeferredMemoryWorkRetryPolicy{MaxDelay: -1},
		},
		{
			name:   "negative backoff factor",
			policy: DeferredMemoryWorkRetryPolicy{BackoffFactor: -1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewDeferredMemoryWorkRetryDecider(tt.policy); !errors.Is(err, ErrDeferredMemoryWorkRetryPolicyInvalid) {
				t.Fatalf("NewDeferredMemoryWorkRetryDecider() error = %v, want ErrDeferredMemoryWorkRetryPolicyInvalid", err)
			}
		})
	}
}
