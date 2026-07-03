package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
)

func TestWorkerRunOnceCompletesSuccessfulJob(t *testing.T) {
	queue := NewMemoryQueue(Job{
		ID:       "job-1",
		Payload:  "ship",
		Attempt:  1,
		Metadata: map[string]any{"queue": "default"},
	})
	worker, err := NewWorker(queue, HandlerFunc(func(_ context.Context, job Job) (Result, error) {
		if job.ID != "job-1" || job.Payload != "ship" || job.Metadata["queue"] != "default" {
			t.Fatalf("job = %+v, want copied job-1", job)
		}
		return Result{Status: JobSucceeded, Output: "ok", Metadata: map[string]any{"worker": "local"}}, nil
	}))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Dequeued || result.Job.ID != "job-1" || result.Result.Status != JobSucceeded {
		t.Fatalf("result = %+v, want completed job-1", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Completed) != 1 || snapshot.Completed[0].Job.ID != "job-1" || snapshot.Completed[0].Result.Output != "ok" {
		t.Fatalf("completed snapshot = %+v, want completed job-1", snapshot.Completed)
	}
	if len(snapshot.Pending) != 0 || len(snapshot.Retried) != 0 || len(snapshot.DeadLettered) != 0 {
		t.Fatalf("snapshot = %+v, want only completed transition", snapshot)
	}
}

func TestWorkerRunOnceRetriesFailedJobWithDefaultBackoff(t *testing.T) {
	queue := NewMemoryQueue(Job{
		ID:          "job-1",
		Payload:     "retry",
		Attempt:     1,
		MaxAttempts: 3,
		Metadata:    map[string]any{"queue": "default"},
	})
	recorder := gopact.NewVerificationRecorder()
	handlerErr := errors.New("temporary outage")
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{}, handlerErr
	}), WithRecorder(recorder))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Result.Status != JobFailed ||
		result.Decision.Action != ScheduleRetry ||
		result.Decision.Attempt != 1 ||
		result.Decision.NextAttempt != 2 ||
		result.Decision.MaxAttempts != 3 ||
		result.Decision.Delay <= 0 {
		t.Fatalf("result = %+v, want retry decision", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Retried) != 1 || len(snapshot.Pending) != 1 {
		t.Fatalf("snapshot = %+v, want retry transition and pending retry", snapshot)
	}
	if snapshot.Pending[0].Attempt != 2 || snapshot.Pending[0].Metadata["queue"] != "default" || snapshot.Pending[0].Metadata["job_id"] != "job-1" {
		t.Fatalf("pending retry = %+v, want attempt 2 copied metadata", snapshot.Pending[0])
	}
	checks := recorder.Checks()
	if len(checks) != 1 ||
		checks[0].Status != gopact.VerificationStatusPassed ||
		checks[0].Evidence[0].Type != VerificationEvidenceTypeSchedule {
		t.Fatalf("checks = %+v, want passed schedule evidence", checks)
	}
}

func TestWorkerRunOnceDeadLettersAtMaxAttempts(t *testing.T) {
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 2, MaxAttempts: 2})
	recorder := gopact.NewVerificationRecorder()
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{}, errors.New("permanent failure")
	}), WithRecorder(recorder))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if !errors.Is(err, ErrJobDeadLettered) {
		t.Fatalf("RunOnce() error = %v, want ErrJobDeadLettered", err)
	}
	if result.Decision.Action != ScheduleDeadLetter || result.Decision.Attempt != 2 {
		t.Fatalf("result = %+v, want dead-letter attempt 2", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.DeadLettered) != 1 || snapshot.DeadLettered[0].Job.ID != "job-1" {
		t.Fatalf("dead-letter snapshot = %+v, want job-1", snapshot.DeadLettered)
	}
	checks := recorder.Checks()
	if len(checks) != 1 || checks[0].Status != gopact.VerificationStatusFailed {
		t.Fatalf("checks = %+v, want failed schedule evidence", checks)
	}
}

func TestWorkerRunOnceDoesNotDequeueWhenLeaseConflicts(t *testing.T) {
	ctx := context.Background()
	leases := gopact.NewMemoryLeaseBackend()
	if _, err := leases.AcquireLease(ctx, gopact.LeaseRequest{Key: "scheduler/default", Owner: "other", TTL: time.Minute}); err != nil {
		t.Fatalf("AcquireLease(other) error = %v", err)
	}
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 1})
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		t.Fatal("handler should not run without lease")
		return Result{}, nil
	}), WithLease(leases, gopact.LeaseRequest{Key: "scheduler/default", Owner: "worker", TTL: time.Minute}))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	result, err := worker.RunOnce(ctx)
	if !errors.Is(err, gopact.ErrLeaseConflict) {
		t.Fatalf("RunOnce() error = %v, want ErrLeaseConflict", err)
	}
	if result.Dequeued {
		t.Fatalf("result = %+v, want no dequeue without lease", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Pending) != 1 || snapshot.Pending[0].ID != "job-1" {
		t.Fatalf("snapshot = %+v, want job still pending", snapshot)
	}
}

func TestWorkerRunOnceReleasesLeaseAfterSuccess(t *testing.T) {
	ctx := context.Background()
	leases := gopact.NewMemoryLeaseBackend()
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 1})
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{Status: JobSucceeded}, nil
	}), WithLease(leases, gopact.LeaseRequest{Key: "scheduler/default", Owner: "worker", TTL: time.Minute}))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if lease, ok, err := leases.GetLease(ctx, "scheduler/default"); err != nil || ok {
		t.Fatalf("GetLease() lease=%+v ok=%v err=%v, want released lease", lease, ok, err)
	}
}

func TestWorkerRenewsLeaseWhileJobRuns(t *testing.T) {
	ctx := context.Background()
	leases := newCountingLeaseBackend(gopact.NewMemoryLeaseBackend())
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 1})
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		select {
		case <-leases.renewed:
			return Result{Status: JobSucceeded}, nil
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for lease renewal")
			return Result{}, nil
		}
	}),
		WithLease(leases, gopact.LeaseRequest{Key: "scheduler/default", Owner: "worker", TTL: time.Minute}),
		WithLeaseRenewalInterval(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if leases.RenewCount() == 0 {
		t.Fatal("renew count = 0, want renewal while job runs")
	}
	if lease, ok, err := leases.GetLease(ctx, "scheduler/default"); err != nil || ok {
		t.Fatalf("GetLease() lease=%+v ok=%v err=%v, want released renewed lease", lease, ok, err)
	}
}

func TestWorkerDoesNotCommitAfterLeaseRenewalFails(t *testing.T) {
	ctx := context.Background()
	leases := newFailingRenewLeaseBackend(gopact.NewMemoryLeaseBackend())
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 1})
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		select {
		case <-leases.renewAttempted:
			return Result{Status: JobSucceeded}, nil
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for failed renewal")
			return Result{}, nil
		}
	}),
		WithLease(leases, gopact.LeaseRequest{Key: "scheduler/default", Owner: "worker", TTL: time.Minute}),
		WithLeaseRenewalInterval(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	result, err := worker.RunOnce(ctx)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("RunOnce() result=%+v error=%v, want ErrLeaseLost", result, err)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Completed) != 0 {
		t.Fatalf("completed snapshot = %+v, want no commit after lease loss", snapshot.Completed)
	}
	if lease, ok, err := leases.GetLease(ctx, "scheduler/default"); err != nil || ok {
		t.Fatalf("GetLease() lease=%+v ok=%v err=%v, want released lease", lease, ok, err)
	}
}

func TestWorkerRunOnceStopsFailedJobWithCustomDecider(t *testing.T) {
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 1})
	worker, err := NewWorker(queue,
		HandlerFunc(func(context.Context, Job) (Result, error) {
			return Result{}, errors.New("not retryable")
		}),
		WithScheduleDecider(ScheduleDeciderFunc(func(_ context.Context, request ScheduleRequest) (ScheduleDecision, error) {
			return ScheduleDecision{
				Action:  ScheduleStop,
				Attempt: request.Attempt,
				Reason:  "manual stop",
			}, nil
		})),
	)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if !errors.Is(err, ErrJobStoppedAfterFailure) {
		t.Fatalf("RunOnce() error = %v, want ErrJobStoppedAfterFailure", err)
	}
	if result.Decision.Action != ScheduleStop || result.Decision.Reason != "manual stop" {
		t.Fatalf("result = %+v, want stop decision", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Stopped) != 1 || snapshot.Stopped[0].Job.ID != "job-1" {
		t.Fatalf("stopped snapshot = %+v, want stopped job-1", snapshot.Stopped)
	}
}

func TestWorkerRunOnceNormalizesCustomRetryDecision(t *testing.T) {
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 2})
	recorder := gopact.NewVerificationRecorder()
	worker, err := NewWorker(queue,
		HandlerFunc(func(context.Context, Job) (Result, error) {
			return Result{}, errors.New("retryable")
		}),
		WithScheduleDecider(ScheduleDeciderFunc(func(context.Context, ScheduleRequest) (ScheduleDecision, error) {
			return ScheduleDecision{Action: ScheduleRetry, Delay: time.Second, Reason: "custom retry"}, nil
		})),
		WithRecorder(recorder),
	)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Decision.Attempt != 2 || result.Decision.NextAttempt != 3 {
		t.Fatalf("decision = %+v, want normalized retry attempt 2 -> 3", result.Decision)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Pending) != 1 || snapshot.Pending[0].Attempt != 3 {
		t.Fatalf("pending = %+v, want normalized attempt 3", snapshot.Pending)
	}
	checks := recorder.Checks()
	if len(checks) != 1 || checks[0].Evidence[0].Metadata["next_attempt"] != 3 {
		t.Fatalf("checks = %+v, want retry evidence with next_attempt 3", checks)
	}
}

func TestWorkerDrainCountsCompletedJobsAndStopsOnEmpty(t *testing.T) {
	queue := NewMemoryQueue(
		Job{ID: "job-1", Attempt: 1},
		Job{ID: "job-2", Attempt: 1},
	)
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{Status: JobSucceeded}, nil
	}))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	drain, err := worker.Drain(context.Background(), 5)
	if err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if drain.Dequeued != 2 || drain.Completed != 2 || len(drain.Results) != 2 {
		t.Fatalf("drain = %+v, want two completed jobs", drain)
	}
}

func TestWorkerDrainCountsRetryAndStopsOnEmpty(t *testing.T) {
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 1})
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{}, errors.New("retryable")
	}))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	drain, err := worker.Drain(context.Background(), 5)
	if err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if drain.Dequeued != 1 || drain.Retried != 1 || drain.Completed != 0 {
		t.Fatalf("drain = %+v, want one retried job", drain)
	}
}

func TestRetryDeciderUsesPolicyAndCapsDelay(t *testing.T) {
	decider, err := NewRetryDecider(RetryPolicy{
		MaxAttempts:   5,
		InitialDelay:  time.Second,
		MaxDelay:      3 * time.Second,
		BackoffFactor: 4,
		Reason:        "policy retry",
		Metadata:      map[string]any{"policy": "background"},
	})
	if err != nil {
		t.Fatalf("NewRetryDecider() error = %v", err)
	}

	decision, err := decider.DecideSchedule(context.Background(), ScheduleRequest{
		Job:      Job{ID: "job-1"},
		Result:   Result{Status: JobFailed},
		Attempt:  3,
		Metadata: map[string]any{"queue": "default"},
	})
	if err != nil {
		t.Fatalf("DecideSchedule(retry) error = %v", err)
	}
	if decision.Action != ScheduleRetry ||
		decision.NextAttempt != 4 ||
		decision.Delay != 3*time.Second ||
		decision.Metadata["queue"] != "default" ||
		decision.Metadata["policy"] != "background" {
		t.Fatalf("decision = %+v, want capped policy retry", decision)
	}

	decision, err = decider.DecideSchedule(context.Background(), ScheduleRequest{
		Job:     Job{ID: "job-1"},
		Result:  Result{Status: JobFailed},
		Attempt: 5,
	})
	if err != nil {
		t.Fatalf("DecideSchedule(dead-letter) error = %v", err)
	}
	if decision.Action != ScheduleDeadLetter || decision.Reason != "policy retry" {
		t.Fatalf("decision = %+v, want policy dead-letter", decision)
	}
}

func TestMemoryQueueHonorsNotBeforeWithInjectedClock(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	queue := NewMemoryQueueWithOptions([]Job{
		{ID: "future", Attempt: 1, NotBefore: now.Add(time.Minute)},
	}, WithMemoryQueueClock(func() time.Time { return now }))

	if job, ok, err := queue.Dequeue(context.Background()); err != nil || ok {
		t.Fatalf("Dequeue(before) job=%+v ok=%v err=%v, want no ready job", job, ok, err)
	}
	now = now.Add(time.Minute)
	job, ok, err := queue.Dequeue(context.Background())
	if err != nil || !ok || job.ID != "future" {
		t.Fatalf("Dequeue(after) job=%+v ok=%v err=%v, want future job", job, ok, err)
	}
}

func TestMemoryQueueSnapshotReturnsDefensiveCopies(t *testing.T) {
	queue := NewMemoryQueue(Job{ID: "job-1", Attempt: 1, Metadata: map[string]any{"queue": "default"}})
	worker, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{Status: JobSucceeded, Metadata: map[string]any{"worker": "one"}}, nil
	}))
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	first := queue.Snapshot()
	first.Completed[0].Job.Metadata["queue"] = "mutated"
	first.Completed[0].Result.Metadata["worker"] = "mutated"

	second := queue.Snapshot()
	if second.Completed[0].Job.Metadata["queue"] != "default" ||
		second.Completed[0].Result.Metadata["worker"] != "one" {
		t.Fatalf("snapshot = %+v, want defensive copies", second)
	}
}

func TestRecordScheduleCheckAndValidationErrors(t *testing.T) {
	if _, err := NewWorker(nil, HandlerFunc(func(context.Context, Job) (Result, error) { return Result{}, nil })); !errors.Is(err, ErrQueueRequired) {
		t.Fatalf("NewWorker(nil queue) error = %v, want ErrQueueRequired", err)
	}
	if _, err := NewWorker(NewMemoryQueue(), nil); !errors.Is(err, ErrHandlerRequired) {
		t.Fatalf("NewWorker(nil handler) error = %v, want ErrHandlerRequired", err)
	}
	if _, err := NewRetryDecider(RetryPolicy{MaxAttempts: -1}); !errors.Is(err, ErrRetryPolicyInvalid) {
		t.Fatalf("NewRetryDecider(invalid) error = %v, want ErrRetryPolicyInvalid", err)
	}
	if _, err := NewWorker(NewMemoryQueue(), HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{}, nil
	}), WithLeaseRenewalInterval(0)); !errors.Is(err, ErrLeaseRenewalIntervalRequired) {
		t.Fatalf("NewWorker(invalid renewal) error = %v, want ErrLeaseRenewalIntervalRequired", err)
	}
	if _, err := NewWorker(NewMemoryQueue(), HandlerFunc(func(context.Context, Job) (Result, error) {
		return Result{}, nil
	}), WithLeaseRenewalInterval(time.Second)); !errors.Is(err, ErrLeaseBackendRequired) {
		t.Fatalf("NewWorker(renewal without lease) error = %v, want ErrLeaseBackendRequired", err)
	}
	if _, err := (HandlerFunc)(nil).HandleJob(context.Background(), Job{}); !errors.Is(err, ErrHandlerRequired) {
		t.Fatalf("nil HandlerFunc error = %v, want ErrHandlerRequired", err)
	}
	if _, err := (ScheduleDeciderFunc)(nil).DecideSchedule(context.Background(), ScheduleRequest{}); !errors.Is(err, ErrScheduleDeciderRequired) {
		t.Fatalf("nil ScheduleDeciderFunc error = %v, want ErrScheduleDeciderRequired", err)
	}

	recorder := gopact.NewVerificationRecorder()
	err := RecordScheduleCheck(recorder, ScheduleDecision{
		Job:     Job{ID: "job-1"},
		Result:  Result{Status: JobFailed, Error: "failed"},
		Action:  ScheduleDeadLetter,
		Attempt: 1,
	})
	if !errors.Is(err, ErrJobDeadLettered) {
		t.Fatalf("RecordScheduleCheck(dead-letter) error = %v, want ErrJobDeadLettered", err)
	}
	checks := recorder.Checks()
	if len(checks) != 1 || checks[0].Status != gopact.VerificationStatusFailed {
		t.Fatalf("checks = %+v, want failed dead-letter evidence", checks)
	}
}

type countingLeaseBackend struct {
	gopact.LeaseBackend
	mu      sync.Mutex
	renews  int
	renewed chan struct{}
}

func newCountingLeaseBackend(next gopact.LeaseBackend) *countingLeaseBackend {
	return &countingLeaseBackend{
		LeaseBackend: next,
		renewed:      make(chan struct{}, 1),
	}
}

func (b *countingLeaseBackend) RenewLease(ctx context.Context, request gopact.LeaseRenewRequest) (gopact.LeaseRecord, error) {
	record, err := b.LeaseBackend.RenewLease(ctx, request)
	if err != nil {
		return gopact.LeaseRecord{}, err
	}
	b.mu.Lock()
	b.renews++
	b.mu.Unlock()
	select {
	case b.renewed <- struct{}{}:
	default:
	}
	return record, nil
}

func (b *countingLeaseBackend) RenewCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.renews
}

type failingRenewLeaseBackend struct {
	gopact.LeaseBackend
	renewAttempted chan struct{}
}

func newFailingRenewLeaseBackend(next gopact.LeaseBackend) *failingRenewLeaseBackend {
	return &failingRenewLeaseBackend{
		LeaseBackend:   next,
		renewAttempted: make(chan struct{}),
	}
}

func (b *failingRenewLeaseBackend) RenewLease(context.Context, gopact.LeaseRenewRequest) (gopact.LeaseRecord, error) {
	close(b.renewAttempted)
	return gopact.LeaseRecord{}, errors.New("renew failed")
}
