package react

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/memory"
)

func TestDeferredMemoryWorkWorkerRunOnceCompletesSuccessfulJob(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	queue := &fakeDeferredMemoryWorkQueue{
		job: DeferredMemoryWorkJob{
			ID:      "job-1",
			Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-1", "memory:pending-1", "remember success")}),
			Attempt: 1,
		},
		hasJob: true,
	}
	worker, err := NewDeferredMemoryWorkWorker(queue, memory.NewReplayHandler(store))
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if !result.Dequeued || result.Job.ID != "job-1" {
		t.Fatalf("result = %+v, want dequeued job-1", result)
	}
	if result.Report.Status != DeferredMemoryWorkSucceeded {
		t.Fatalf("result report status = %q, want succeeded", result.Report.Status)
	}
	if queue.completedJob.ID != "job-1" || queue.completedReport.Status != DeferredMemoryWorkSucceeded {
		t.Fatalf("completed job/report = %+v/%+v, want job-1 succeeded", queue.completedJob, queue.completedReport)
	}
	if queue.retriedJob.ID != "" || queue.deadLetteredJob.ID != "" || queue.stoppedJob.ID != "" {
		t.Fatalf("unexpected terminal queues: retry=%+v dead=%+v stop=%+v", queue.retriedJob, queue.deadLetteredJob, queue.stoppedJob)
	}
	stored, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "remember success",
	})
	if err != nil {
		t.Fatalf("Search(memory) error = %v", err)
	}
	if len(stored.Memories) != 1 {
		t.Fatalf("stored memories = %+v, want one replayed memory", stored.Memories)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceRetriesFailedJobAndRecordsSchedule(t *testing.T) {
	queue := &fakeDeferredMemoryWorkQueue{
		job: DeferredMemoryWorkJob{
			ID:          "job-1",
			Export:      deferredMemoryWorkExport(twoPendingMemoryEffects()),
			Attempt:     1,
			MaxAttempts: 3,
			Metadata:    map[string]any{"queue": "memory-default"},
		},
		hasJob: true,
	}
	recorder := gopact.NewVerificationRecorder()
	worker, err := NewDeferredMemoryWorkWorker(queue, failingSecondMemoryEffectExecutor(),
		WithDeferredMemoryWorkScheduleDecider(DeferredMemoryWorkScheduleDeciderFunc(func(_ context.Context, request DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error) {
			if request.Job.ID != "job-1" || request.Attempt != 1 || request.Report.Status != DeferredMemoryWorkFailed {
				t.Fatalf("schedule request = %+v, want failed job-1 attempt 1", request)
			}
			return DeferredMemoryWorkScheduleDecision{
				Action:      DeferredMemoryWorkScheduleRetry,
				NextAttempt: 2,
				Delay:       10 * time.Millisecond,
				Reason:      "temporary store outage",
			}, nil
		})),
		WithDeferredMemoryWorkScheduleRecorder(recorder),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if result.Report.Status != DeferredMemoryWorkFailed {
		t.Fatalf("result report status = %q, want failed", result.Report.Status)
	}
	if queue.retriedJob.ID != "job-1" {
		t.Fatalf("retried job = %+v, want job-1", queue.retriedJob)
	}
	if queue.retryDecision.Action != DeferredMemoryWorkScheduleRetry ||
		queue.retryDecision.Attempt != 1 ||
		queue.retryDecision.NextAttempt != 2 ||
		queue.retryDecision.MaxAttempts != 3 ||
		queue.retryDecision.Report.Status != DeferredMemoryWorkFailed ||
		queue.retryDecision.Metadata["queue"] != "memory-default" {
		t.Fatalf("retry decision = %+v, want normalized retry decision", queue.retryDecision)
	}
	checks := recorder.Checks()
	if len(checks) != 1 || checks[0].Evidence[0].Type != VerificationEvidenceTypeDeferredMemoryWorkSchedule {
		t.Fatalf("recorded checks = %+v, want one memory_work_schedule evidence", checks)
	}
	if checks[0].Status != gopact.VerificationStatusPassed {
		t.Fatalf("recorded check status = %q, want passed", checks[0].Status)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceRecordsFailedPassBeforeRetrySchedule(t *testing.T) {
	queue := &fakeDeferredMemoryWorkQueue{
		job: DeferredMemoryWorkJob{
			ID:          "job-1",
			Export:      deferredMemoryWorkExport(twoPendingMemoryEffects()),
			Attempt:     1,
			MaxAttempts: 3,
			Metadata:    map[string]any{"queue": "memory-default"},
		},
		hasJob: true,
	}
	recorder := gopact.NewVerificationRecorder()
	worker, err := NewDeferredMemoryWorkWorker(queue, failingSecondMemoryEffectExecutor(),
		WithDeferredMemoryWorkReportRecorder(recorder),
		WithDeferredMemoryWorkScheduleRecorder(recorder),
		WithDeferredMemoryWorkScheduleDecider(DeferredMemoryWorkScheduleDeciderFunc(func(_ context.Context, request DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error) {
			return DeferredMemoryWorkScheduleDecision{
				Action:      DeferredMemoryWorkScheduleRetry,
				NextAttempt: request.Attempt + 1,
				Delay:       10 * time.Millisecond,
				Reason:      "temporary store outage",
			}, nil
		})),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v, want retry scheduled without terminal error", err)
	}

	if result.Report.Status != DeferredMemoryWorkFailed ||
		result.Decision.Action != DeferredMemoryWorkScheduleRetry ||
		queue.retriedJob.ID != "job-1" {
		t.Fatalf("result=%+v retried=%+v, want failed pass with retry transition", result, queue.retriedJob)
	}
	checks := recorder.Checks()
	if len(checks) != 2 {
		t.Fatalf("recorded checks = %+v, want memory replay check then schedule check", checks)
	}
	if checks[0].Status != gopact.VerificationStatusFailed ||
		len(checks[0].Evidence) != 1 ||
		checks[0].Evidence[0].Type != memory.VerificationEvidenceTypeMemoryReplay {
		t.Fatalf("first recorded check = %+v, want failed memory replay evidence", checks[0])
	}
	if checks[1].Status != gopact.VerificationStatusPassed ||
		len(checks[1].Evidence) != 1 ||
		checks[1].Evidence[0].Type != VerificationEvidenceTypeDeferredMemoryWorkSchedule {
		t.Fatalf("second recorded check = %+v, want passed schedule evidence", checks[1])
	}
}

func TestDeferredMemoryWorkWorkerRunOnceUnifiedRecorderCapturesPassAndSchedule(t *testing.T) {
	queue := &fakeDeferredMemoryWorkQueue{
		job: DeferredMemoryWorkJob{
			ID:          "job-1",
			Export:      deferredMemoryWorkExport(twoPendingMemoryEffects()),
			Attempt:     1,
			MaxAttempts: 3,
		},
		hasJob: true,
	}
	recorder := gopact.NewVerificationRecorder()
	worker, err := NewDeferredMemoryWorkWorker(queue, failingSecondMemoryEffectExecutor(),
		WithDeferredMemoryWorkRecorder(recorder),
		WithDeferredMemoryWorkScheduleDecider(DeferredMemoryWorkScheduleDeciderFunc(func(_ context.Context, request DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error) {
			return DeferredMemoryWorkScheduleDecision{
				Action:      DeferredMemoryWorkScheduleRetry,
				NextAttempt: request.Attempt + 1,
				Reason:      "temporary store outage",
			}, nil
		})),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v, want retry scheduled without terminal error", err)
	}

	checks := recorder.Checks()
	if len(checks) != 2 {
		t.Fatalf("recorded checks = %+v, want memory replay and schedule evidence", checks)
	}
	if checks[0].Evidence[0].Type != memory.VerificationEvidenceTypeMemoryReplay ||
		checks[1].Evidence[0].Type != VerificationEvidenceTypeDeferredMemoryWorkSchedule {
		t.Fatalf("recorded checks = %+v, want replay evidence before schedule evidence", checks)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceUsesDefaultRetryDecider(t *testing.T) {
	queue := NewMemoryDeferredMemoryWorkQueue(DeferredMemoryWorkJob{
		ID:          "job-1",
		Export:      deferredMemoryWorkExport(twoPendingMemoryEffects()),
		Attempt:     1,
		MaxAttempts: 2,
		Metadata:    map[string]any{"queue": "local"},
	})
	recorder := gopact.NewVerificationRecorder()
	worker, err := NewDeferredMemoryWorkWorker(queue, failingSecondMemoryEffectExecutor(),
		WithDeferredMemoryWorkScheduleRecorder(recorder),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if result.Decision.Action != DeferredMemoryWorkScheduleRetry ||
		result.Decision.NextAttempt != 2 ||
		result.Decision.MaxAttempts != 2 ||
		result.Decision.Delay <= 0 {
		t.Fatalf("result decision = %+v, want default retry decision", result.Decision)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Retried) != 1 || len(snapshot.Pending) != 1 {
		t.Fatalf("queue snapshot = %+v, want one retry record and one pending retry", snapshot)
	}
	if snapshot.Pending[0].Attempt != 2 ||
		snapshot.Pending[0].Metadata["queue"] != "local" ||
		snapshot.Pending[0].Metadata["job_id"] != "job-1" {
		t.Fatalf("pending retry job = %+v, want copied attempt 2 with metadata", snapshot.Pending[0])
	}
	checks := recorder.Checks()
	if len(checks) != 1 ||
		checks[0].Status != gopact.VerificationStatusPassed ||
		checks[0].Evidence[0].Type != VerificationEvidenceTypeDeferredMemoryWorkSchedule {
		t.Fatalf("recorded checks = %+v, want passed schedule evidence", checks)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceDeadLettersFailedJob(t *testing.T) {
	queue := &fakeDeferredMemoryWorkQueue{
		job: DeferredMemoryWorkJob{
			ID:          "job-1",
			Export:      deferredMemoryWorkExport(twoPendingMemoryEffects()),
			Attempt:     3,
			MaxAttempts: 3,
		},
		hasJob: true,
	}
	recorder := gopact.NewVerificationRecorder()
	worker, err := NewDeferredMemoryWorkWorker(queue, failingSecondMemoryEffectExecutor(),
		WithDeferredMemoryWorkScheduleDecider(DeferredMemoryWorkScheduleDeciderFunc(func(_ context.Context, request DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error) {
			return DeferredMemoryWorkScheduleDecision{
				Action: DeferredMemoryWorkScheduleDeadLetter,
				Reason: "max attempts reached",
			}, nil
		})),
		WithDeferredMemoryWorkScheduleRecorder(recorder),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(context.Background())
	if !errors.Is(err, ErrDeferredMemoryWorkDeadLettered) {
		t.Fatalf("RunOnce() error = %v, want ErrDeferredMemoryWorkDeadLettered", err)
	}
	if result.Decision.Action != DeferredMemoryWorkScheduleDeadLetter {
		t.Fatalf("result decision = %+v, want dead-letter", result.Decision)
	}
	if queue.deadLetteredJob.ID != "job-1" || queue.deadLetterDecision.Action != DeferredMemoryWorkScheduleDeadLetter {
		t.Fatalf("dead-lettered = %+v/%+v, want job-1 dead-letter", queue.deadLetteredJob, queue.deadLetterDecision)
	}
	checks := recorder.Checks()
	if len(checks) != 1 || checks[0].Status != gopact.VerificationStatusFailed {
		t.Fatalf("recorded checks = %+v, want failed schedule check", checks)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceHandlesEmptyQueueAndInvalidConfig(t *testing.T) {
	queue := &fakeDeferredMemoryWorkQueue{}
	worker, err := NewDeferredMemoryWorkWorker(queue, memory.NewReplayHandler(memory.New()))
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}
	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce(empty) error = %v", err)
	}
	if result.Dequeued {
		t.Fatalf("result = %+v, want no dequeued job", result)
	}

	if _, err := NewDeferredMemoryWorkWorker(nil, memory.NewReplayHandler(memory.New())); !errors.Is(err, ErrDeferredMemoryWorkQueueRequired) {
		t.Fatalf("NewDeferredMemoryWorkWorker(nil queue) error = %v, want ErrDeferredMemoryWorkQueueRequired", err)
	}
	if _, err := NewDeferredMemoryWorkWorker(queue, nil); !errors.Is(err, ErrMemoryWorkExecutorRequired) {
		t.Fatalf("NewDeferredMemoryWorkWorker(nil executor) error = %v, want ErrMemoryWorkExecutorRequired", err)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceUsesLeaseGate(t *testing.T) {
	ctx := context.Background()
	key := "react/memory-worker"
	leases := gopact.NewMemoryLeaseBackend()
	if _, err := leases.AcquireLease(ctx, gopact.LeaseRequest{Key: key, Owner: "worker-b", TTL: time.Minute}); err != nil {
		t.Fatalf("AcquireLease(conflicting) error = %v", err)
	}
	queue := NewMemoryDeferredMemoryWorkQueue(DeferredMemoryWorkJob{
		ID:      "job-1",
		Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-1", "memory:pending-1", "leased")}),
		Attempt: 1,
	})
	worker, err := NewDeferredMemoryWorkWorker(queue, memory.NewReplayHandler(memory.New()),
		WithDeferredMemoryWorkLease(leases, gopact.LeaseRequest{Key: key, Owner: "worker-a", TTL: time.Minute}),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(ctx)
	if !errors.Is(err, gopact.ErrLeaseConflict) {
		t.Fatalf("RunOnce() error = %v, want ErrLeaseConflict", err)
	}
	if result.Dequeued {
		t.Fatalf("RunOnce() result = %+v, want no dequeue when lease conflicts", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Pending) != 1 || len(snapshot.Completed) != 0 {
		t.Fatalf("queue snapshot = %+v, want untouched pending job", snapshot)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceReleasesLeaseAfterPass(t *testing.T) {
	ctx := context.Background()
	key := "react/memory-worker"
	leases := gopact.NewMemoryLeaseBackend()
	queue := NewMemoryDeferredMemoryWorkQueue(DeferredMemoryWorkJob{
		ID:      "job-1",
		Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-1", "memory:pending-1", "leased")}),
		Attempt: 1,
	})
	worker, err := NewDeferredMemoryWorkWorker(queue, memory.NewReplayHandler(memory.New()),
		WithDeferredMemoryWorkLease(leases, gopact.LeaseRequest{Key: key, Owner: "worker-a", TTL: time.Minute}),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if lease, ok, err := leases.GetLease(ctx, key); err != nil || ok {
		t.Fatalf("GetLease(after RunOnce) lease=%+v ok=%v err=%v, want released", lease, ok, err)
	}
	if _, err := leases.AcquireLease(ctx, gopact.LeaseRequest{Key: key, Owner: "worker-b", TTL: time.Minute}); err != nil {
		t.Fatalf("AcquireLease(after RunOnce) error = %v, want released lease", err)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceRenewsLeaseDuringPass(t *testing.T) {
	ctx := context.Background()
	key := "react/memory-worker"
	leases := newReactCountingLeaseBackend(gopact.NewMemoryLeaseBackend())
	queue := NewMemoryDeferredMemoryWorkQueue(DeferredMemoryWorkJob{
		ID:      "job-1",
		Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-1", "memory:pending-1", "leased")}),
		Attempt: 1,
	})
	executor := gopact.EffectReplayFunc(func(ctx context.Context, decision gopact.EffectReplayDecision) (gopact.EffectReplayResult, error) {
		leases.WaitRenew(t, time.Second)
		return gopact.EffectReplayResult{EffectID: decision.Effect.ID, Action: decision.Action, ReplayPolicy: decision.ReplayPolicy}, nil
	})
	worker, err := NewDeferredMemoryWorkWorker(queue, executor,
		WithDeferredMemoryWorkLease(leases, gopact.LeaseRequest{Key: key, Owner: "worker-a", TTL: time.Second}),
		WithDeferredMemoryWorkLeaseRenewalInterval(5*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if lease, ok, err := leases.GetLease(ctx, key); err != nil || ok {
		t.Fatalf("GetLease(after renewed RunOnce) lease=%+v ok=%v err=%v, want released", lease, ok, err)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Completed) != 1 {
		t.Fatalf("queue snapshot = %+v, want completed job after renewed pass", snapshot)
	}
}

func TestDeferredMemoryWorkWorkerRunOnceWaitsForRenewalRecordUpdate(t *testing.T) {
	ctx := context.Background()
	key := "react/memory-worker"
	leases := newReactDelayedRenewLeaseBackend(gopact.NewMemoryLeaseBackend(), 50*time.Millisecond)
	queue := NewMemoryDeferredMemoryWorkQueue(DeferredMemoryWorkJob{
		ID:      "job-1",
		Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-1", "memory:pending-1", "leased")}),
		Attempt: 1,
	})
	executor := gopact.EffectReplayFunc(func(ctx context.Context, decision gopact.EffectReplayDecision) (gopact.EffectReplayResult, error) {
		leases.WaitRenewStarted(t, time.Second)
		return gopact.EffectReplayResult{EffectID: decision.Effect.ID, Action: decision.Action, ReplayPolicy: decision.ReplayPolicy}, nil
	})
	worker, err := NewDeferredMemoryWorkWorker(queue, executor,
		WithDeferredMemoryWorkLease(leases, gopact.LeaseRequest{Key: key, Owner: "worker-a", TTL: time.Second}),
		WithDeferredMemoryWorkLeaseRenewalInterval(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() result=%+v error=%v snapshot=%+v", result, err, queue.Snapshot())
	}
}

func TestDeferredMemoryWorkWorkerRunOnceStopsWhenLeaseRenewalLost(t *testing.T) {
	ctx := context.Background()
	key := "react/memory-worker"
	leases := newReactLosingRenewLeaseBackend(gopact.NewMemoryLeaseBackend())
	queue := NewMemoryDeferredMemoryWorkQueue(DeferredMemoryWorkJob{
		ID:      "job-1",
		Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-1", "memory:pending-1", "leased")}),
		Attempt: 1,
	})
	executor := gopact.EffectReplayFunc(func(ctx context.Context, decision gopact.EffectReplayDecision) (gopact.EffectReplayResult, error) {
		leases.WaitRenew(t, time.Second)
		return gopact.EffectReplayResult{EffectID: decision.Effect.ID, Action: decision.Action, ReplayPolicy: decision.ReplayPolicy}, nil
	})
	worker, err := NewDeferredMemoryWorkWorker(queue, executor,
		WithDeferredMemoryWorkLease(leases, gopact.LeaseRequest{Key: key, Owner: "worker-a", TTL: time.Second}),
		WithDeferredMemoryWorkLeaseRenewalInterval(5*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.RunOnce(ctx)
	if !errors.Is(err, gopact.ErrLeaseNotHeld) {
		t.Fatalf("RunOnce() result=%+v error=%v, want ErrLeaseNotHeld", result, err)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Completed) != 0 || len(snapshot.Pending) != 0 {
		t.Fatalf("queue snapshot = %+v, want dequeued job not completed after lease loss", snapshot)
	}
}

func TestNewDeferredMemoryWorkWorkerRejectsInvalidLeaseConfig(t *testing.T) {
	queue := NewMemoryDeferredMemoryWorkQueue()
	executor := memory.NewReplayHandler(memory.New())

	if worker, err := NewDeferredMemoryWorkWorker(queue, executor,
		WithDeferredMemoryWorkLease(nil, gopact.LeaseRequest{Key: "react/memory-worker", Owner: "worker-a", TTL: time.Minute}),
	); !errors.Is(err, ErrDeferredMemoryWorkLeaseBackendRequired) || worker != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker(nil lease backend) worker=%v err=%v, want ErrDeferredMemoryWorkLeaseBackendRequired", worker, err)
	}
	if worker, err := NewDeferredMemoryWorkWorker(queue, executor,
		WithDeferredMemoryWorkLease(gopact.NewMemoryLeaseBackend(), gopact.LeaseRequest{Owner: "worker-a", TTL: time.Minute}),
	); !errors.Is(err, gopact.ErrLeaseKeyRequired) || worker != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker(empty lease key) worker=%v err=%v, want ErrLeaseKeyRequired", worker, err)
	}
	if worker, err := NewDeferredMemoryWorkWorker(queue, executor,
		WithDeferredMemoryWorkLease(gopact.NewMemoryLeaseBackend(), gopact.LeaseRequest{Key: "react/memory-worker", Owner: "worker-a", TTL: time.Minute}),
		WithDeferredMemoryWorkLeaseRenewalInterval(0),
	); !errors.Is(err, ErrDeferredMemoryWorkLeaseRenewalIntervalRequired) || worker != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker(empty renewal interval) worker=%v err=%v, want ErrDeferredMemoryWorkLeaseRenewalIntervalRequired", worker, err)
	}
}

func TestDeferredMemoryWorkWorkerDrainProcessesUntilEmpty(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	queue := NewMemoryDeferredMemoryWorkQueue(
		DeferredMemoryWorkJob{
			ID:      "job-1",
			Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-1", "memory:pending-1", "first")}),
			Attempt: 1,
		},
		DeferredMemoryWorkJob{
			ID:      "job-2",
			Export:  deferredMemoryWorkExport([]gopact.EffectRecord{pendingMemoryPutEffect("pending-2", "memory:pending-2", "second")}),
			Attempt: 1,
		},
	)
	worker, err := NewDeferredMemoryWorkWorker(queue, memory.NewReplayHandler(store))
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.Drain(ctx, 5)
	if err != nil {
		t.Fatalf("Drain() error = %v", err)
	}

	if result.Dequeued != 2 || result.Completed != 2 || result.Retried != 0 ||
		result.DeadLettered != 0 || result.Stopped != 0 || len(result.Results) != 2 {
		t.Fatalf("drain result = %+v, want two completed jobs", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Pending) != 0 || len(snapshot.Completed) != 2 {
		t.Fatalf("queue snapshot = %+v, want drained completed queue", snapshot)
	}
}

func TestDeferredMemoryWorkWorkerDrainReturnsTerminalErrorWithSummary(t *testing.T) {
	queue := NewMemoryDeferredMemoryWorkQueue(DeferredMemoryWorkJob{
		ID:          "job-1",
		Export:      deferredMemoryWorkExport(twoPendingMemoryEffects()),
		Attempt:     1,
		MaxAttempts: 1,
	})
	worker, err := NewDeferredMemoryWorkWorker(queue, failingSecondMemoryEffectExecutor())
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	result, err := worker.Drain(context.Background(), 5)
	if !errors.Is(err, ErrDeferredMemoryWorkDeadLettered) {
		t.Fatalf("Drain() error = %v, want ErrDeferredMemoryWorkDeadLettered", err)
	}

	if result.Dequeued != 1 || result.Completed != 0 || result.Retried != 0 ||
		result.DeadLettered != 1 || len(result.Results) != 1 {
		t.Fatalf("drain result = %+v, want one dead-lettered job", result)
	}
	snapshot := queue.Snapshot()
	if len(snapshot.Pending) != 0 || len(snapshot.DeadLettered) != 1 {
		t.Fatalf("queue snapshot = %+v, want one dead-lettered job", snapshot)
	}
}

func TestDeferredMemoryWorkWorkerDrainRejectsInvalidLimit(t *testing.T) {
	worker, err := NewDeferredMemoryWorkWorker(NewMemoryDeferredMemoryWorkQueue(), memory.NewReplayHandler(memory.New()))
	if err != nil {
		t.Fatalf("NewDeferredMemoryWorkWorker() error = %v", err)
	}

	if _, err := worker.Drain(context.Background(), 0); !errors.Is(err, ErrDeferredMemoryWorkDrainLimitRequired) {
		t.Fatalf("Drain(limit 0) error = %v, want ErrDeferredMemoryWorkDrainLimitRequired", err)
	}
	if _, err := (*DeferredMemoryWorkWorker)(nil).Drain(context.Background(), 1); !errors.Is(err, ErrDeferredMemoryWorkQueueRequired) {
		t.Fatalf("Drain(nil worker) error = %v, want ErrDeferredMemoryWorkQueueRequired", err)
	}
}

type fakeDeferredMemoryWorkQueue struct {
	job    DeferredMemoryWorkJob
	hasJob bool

	completedJob    DeferredMemoryWorkJob
	completedReport DeferredMemoryWorkReport

	retriedJob    DeferredMemoryWorkJob
	retryDecision DeferredMemoryWorkScheduleDecision

	deadLetteredJob    DeferredMemoryWorkJob
	deadLetterDecision DeferredMemoryWorkScheduleDecision

	stoppedJob       DeferredMemoryWorkJob
	stopDecision     DeferredMemoryWorkScheduleDecision
	stopWorkerReport DeferredMemoryWorkReport
}

func (q *fakeDeferredMemoryWorkQueue) Dequeue(context.Context) (DeferredMemoryWorkJob, bool, error) {
	if !q.hasJob {
		return DeferredMemoryWorkJob{}, false, nil
	}
	q.hasJob = false
	return q.job, true, nil
}

func (q *fakeDeferredMemoryWorkQueue) Complete(_ context.Context, job DeferredMemoryWorkJob, report DeferredMemoryWorkReport) error {
	q.completedJob = job
	q.completedReport = report
	return nil
}

func (q *fakeDeferredMemoryWorkQueue) Retry(_ context.Context, job DeferredMemoryWorkJob, decision DeferredMemoryWorkScheduleDecision) error {
	q.retriedJob = job
	q.retryDecision = decision
	return nil
}

func (q *fakeDeferredMemoryWorkQueue) DeadLetter(_ context.Context, job DeferredMemoryWorkJob, decision DeferredMemoryWorkScheduleDecision) error {
	q.deadLetteredJob = job
	q.deadLetterDecision = decision
	return nil
}

func (q *fakeDeferredMemoryWorkQueue) Stop(_ context.Context, job DeferredMemoryWorkJob, report DeferredMemoryWorkReport, decision DeferredMemoryWorkScheduleDecision) error {
	q.stoppedJob = job
	q.stopWorkerReport = report
	q.stopDecision = decision
	return nil
}

type reactCountingLeaseBackend struct {
	gopact.LeaseBackend
	renewed chan gopact.LeaseRecord
}

func newReactCountingLeaseBackend(next gopact.LeaseBackend) *reactCountingLeaseBackend {
	return &reactCountingLeaseBackend{
		LeaseBackend: next,
		renewed:      make(chan gopact.LeaseRecord, 16),
	}
}

func (b *reactCountingLeaseBackend) RenewLease(ctx context.Context, request gopact.LeaseRenewRequest) (gopact.LeaseRecord, error) {
	record, err := b.LeaseBackend.RenewLease(ctx, request)
	if err == nil {
		select {
		case b.renewed <- record:
		default:
		}
	}
	return record, err
}

func (b *reactCountingLeaseBackend) WaitRenew(t *testing.T, timeout time.Duration) gopact.LeaseRecord {
	t.Helper()
	select {
	case record := <-b.renewed:
		return record
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for lease renewal")
		return gopact.LeaseRecord{}
	}
}

type reactDelayedRenewLeaseBackend struct {
	gopact.LeaseBackend
	delay   time.Duration
	started chan struct{}
	once    sync.Once
}

func newReactDelayedRenewLeaseBackend(next gopact.LeaseBackend, delay time.Duration) *reactDelayedRenewLeaseBackend {
	return &reactDelayedRenewLeaseBackend{
		LeaseBackend: next,
		delay:        delay,
		started:      make(chan struct{}),
	}
}

func (b *reactDelayedRenewLeaseBackend) RenewLease(ctx context.Context, request gopact.LeaseRenewRequest) (gopact.LeaseRecord, error) {
	record, err := b.LeaseBackend.RenewLease(ctx, request)
	if err == nil {
		b.once.Do(func() {
			close(b.started)
		})
		select {
		case <-ctx.Done():
			return gopact.LeaseRecord{}, ctx.Err()
		case <-time.After(b.delay):
		}
	}
	return record, err
}

func (b *reactDelayedRenewLeaseBackend) WaitRenewStarted(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for lease renewal to start")
	}
}

type reactLosingRenewLeaseBackend struct {
	*reactCountingLeaseBackend
	mu   sync.Mutex
	lost bool
}

func newReactLosingRenewLeaseBackend(next gopact.LeaseBackend) *reactLosingRenewLeaseBackend {
	return &reactLosingRenewLeaseBackend{
		reactCountingLeaseBackend: newReactCountingLeaseBackend(next),
	}
}

func (b *reactLosingRenewLeaseBackend) RenewLease(ctx context.Context, request gopact.LeaseRenewRequest) (gopact.LeaseRecord, error) {
	b.mu.Lock()
	b.lost = true
	b.mu.Unlock()
	select {
	case b.renewed <- gopact.LeaseRecord{Key: request.Key, Owner: request.Owner, Token: request.Token}:
	default:
	}
	return gopact.LeaseRecord{}, gopact.ErrLeaseNotHeld
}

func (b *reactLosingRenewLeaseBackend) GetLease(ctx context.Context, key string) (gopact.LeaseRecord, bool, error) {
	b.mu.Lock()
	lost := b.lost
	b.mu.Unlock()
	if lost {
		return gopact.LeaseRecord{}, false, nil
	}
	return b.LeaseBackend.GetLease(ctx, key)
}

func twoPendingMemoryEffects() []gopact.EffectRecord {
	return []gopact.EffectRecord{
		pendingMemoryPutEffect("pending-1", "memory:pending-1", "first memory"),
		pendingMemoryPutEffect("pending-2", "memory:pending-2", "second memory"),
	}
}

func failingSecondMemoryEffectExecutor() gopact.EffectReplayExecutor {
	replayErr := errors.New("worker executor failed")
	return gopact.EffectReplayFunc(func(_ context.Context, decision gopact.EffectReplayDecision) (gopact.EffectReplayResult, error) {
		if decision.Effect.ID == "pending-2" {
			return gopact.EffectReplayResult{}, replayErr
		}
		return gopact.EffectReplayResult{
			EffectID:     decision.Effect.ID,
			Action:       gopact.EffectReplayActionReplay,
			ReplayPolicy: decision.Effect.ReplayPolicy,
			Effect:       decision.Effect,
		}, nil
	})
}
