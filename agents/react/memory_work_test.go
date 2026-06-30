package react

import (
	"context"
	"errors"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/memory"
)

func TestRunDeferredMemoryWorkReturnsObservableReport(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	export := deferredMemoryWorkExport([]gopact.EffectRecord{
		pendingMemoryPutEffect("pending-1", "memory:pending-1", "background worker writes memory"),
	})

	report, err := RunDeferredMemoryWork(ctx, export, memory.NewReplayHandler(store))
	if err != nil {
		t.Fatalf("RunDeferredMemoryWork() error = %v", err)
	}
	if report.Status != DeferredMemoryWorkSucceeded {
		t.Fatalf("report status = %q, want succeeded", report.Status)
	}
	if report.RunID != "run-1" || report.ThreadID != "thread-1" {
		t.Fatalf("report IDs = %q/%q, want run/thread IDs", report.RunID, report.ThreadID)
	}
	if report.ReplayCount != 1 || report.ResultCount != 1 || len(report.Results) != 1 {
		t.Fatalf("report counts/results = %+v, want one replay result", report)
	}
	if report.Results[0].StepID != "step-1" || report.Results[0].Result.EffectID != "pending-1" {
		t.Fatalf("report result = %+v, want step/effect identity", report.Results[0])
	}
	stored, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "background worker",
	})
	if err != nil {
		t.Fatalf("Search(memory) error = %v", err)
	}
	if len(stored.Memories) != 1 {
		t.Fatalf("stored memories = %+v, want one replayed memory", stored.Memories)
	}
}

func TestRunDeferredMemoryWorkReportsPartialResultsOnExecutorError(t *testing.T) {
	ctx := context.Background()
	replayErr := errors.New("worker executor failed")
	export := deferredMemoryWorkExport([]gopact.EffectRecord{
		pendingMemoryPutEffect("pending-1", "memory:pending-1", "first memory"),
		pendingMemoryPutEffect("pending-2", "memory:pending-2", "second memory"),
	})
	executor := gopact.EffectReplayFunc(func(_ context.Context, decision gopact.EffectReplayDecision) (gopact.EffectReplayResult, error) {
		if decision.Effect.ID == "pending-2" {
			return gopact.EffectReplayResult{}, replayErr
		}
		return gopact.EffectReplayResult{}, nil
	})

	report, err := RunDeferredMemoryWork(ctx, export, executor)
	if !errors.Is(err, replayErr) {
		t.Fatalf("RunDeferredMemoryWork() error = %v, want replayErr", err)
	}
	if report.Status != DeferredMemoryWorkFailed {
		t.Fatalf("report status = %q, want failed", report.Status)
	}
	if report.ReplayCount != 2 || report.ResultCount != 1 || len(report.Results) != 1 {
		t.Fatalf("report counts/results = %+v, want one partial result from two planned replays", report)
	}
	if report.Results[0].Result.EffectID != "pending-1" {
		t.Fatalf("partial result = %+v, want first effect only", report.Results[0])
	}
	if report.Error == "" {
		t.Fatal("report error summary is empty")
	}
}

func TestRunDeferredMemoryWorkSkipsEmptyPlanWithoutExecutor(t *testing.T) {
	export := deferredMemoryWorkExport(nil)

	report, err := RunDeferredMemoryWork(context.Background(), export, nil)
	if err != nil {
		t.Fatalf("RunDeferredMemoryWork(empty) error = %v", err)
	}
	if report.Status != DeferredMemoryWorkSkipped {
		t.Fatalf("report status = %q, want skipped", report.Status)
	}
	if report.ReplayCount != 0 || report.ResultCount != 0 || len(report.Results) != 0 {
		t.Fatalf("report = %+v, want no replay work", report)
	}
}

func TestRecordDeferredMemoryWorkCheckRecordsWorkerReportEvidence(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	export := deferredMemoryWorkExport([]gopact.EffectRecord{
		pendingMemoryPutEffect("pending-1", "memory:pending-1", "background worker writes memory"),
	})
	report, err := RunDeferredMemoryWork(ctx, export, memory.NewReplayHandler(store))
	if err != nil {
		t.Fatalf("RunDeferredMemoryWork() error = %v", err)
	}

	recorder := gopact.NewVerificationRecorder()
	if err := RecordDeferredMemoryWorkCheck(recorder, report); err != nil {
		t.Fatalf("RecordDeferredMemoryWorkCheck() error = %v", err)
	}

	checks := recorder.Checks()
	if len(checks) != 1 {
		t.Fatalf("recorded checks = %d, want 1", len(checks))
	}
	check := checks[0]
	if check.ID != VerificationCheckDeferredMemoryWork+":run-1" {
		t.Fatalf("check ID = %q, want deferred memory work check", check.ID)
	}
	if check.Status != gopact.VerificationStatusPassed {
		t.Fatalf("check status = %q, want passed", check.Status)
	}
	if len(check.Evidence) != 1 || check.Evidence[0].Type != memory.VerificationEvidenceTypeMemoryReplay {
		t.Fatalf("check evidence = %+v, want memory replay evidence", check.Evidence)
	}
	assertMemoryWorkMetadata(t, check.Metadata, "succeeded", 1, 1)
}

func TestRecordDeferredMemoryWorkCheckReturnsFailureForFailedReport(t *testing.T) {
	replayErr := errors.New("worker executor failed")
	export := deferredMemoryWorkExport([]gopact.EffectRecord{
		pendingMemoryPutEffect("pending-1", "memory:pending-1", "first memory"),
		pendingMemoryPutEffect("pending-2", "memory:pending-2", "second memory"),
	})
	executor := gopact.EffectReplayFunc(func(_ context.Context, decision gopact.EffectReplayDecision) (gopact.EffectReplayResult, error) {
		if decision.Effect.ID == "pending-2" {
			return gopact.EffectReplayResult{}, replayErr
		}
		return gopact.EffectReplayResult{}, nil
	})
	report, err := RunDeferredMemoryWork(context.Background(), export, executor)
	if !errors.Is(err, replayErr) {
		t.Fatalf("RunDeferredMemoryWork() error = %v, want replayErr", err)
	}

	recorder := gopact.NewVerificationRecorder()
	err = RecordDeferredMemoryWorkCheck(recorder, report)
	if !errors.Is(err, memory.ErrReplayVerificationFailed) {
		t.Fatalf("RecordDeferredMemoryWorkCheck() error = %v, want replay verification failure", err)
	}

	checks := recorder.Checks()
	if len(checks) != 1 {
		t.Fatalf("recorded checks = %d, want 1", len(checks))
	}
	check := checks[0]
	if check.Status != gopact.VerificationStatusFailed {
		t.Fatalf("check status = %q, want failed", check.Status)
	}
	assertMemoryWorkMetadata(t, check.Metadata, "failed", 2, 1)
	if check.Metadata["error"] == "" {
		t.Fatalf("check metadata = %+v, want error summary", check.Metadata)
	}
}

func TestRecordDeferredMemoryWorkCheckSkipsEmptyReport(t *testing.T) {
	export := deferredMemoryWorkExport(nil)
	report, err := RunDeferredMemoryWork(context.Background(), export, nil)
	if err != nil {
		t.Fatalf("RunDeferredMemoryWork(empty) error = %v", err)
	}

	recorder := gopact.NewVerificationRecorder()
	if err := RecordDeferredMemoryWorkCheck(recorder, report); err != nil {
		t.Fatalf("RecordDeferredMemoryWorkCheck(skipped) error = %v", err)
	}

	checks := recorder.Checks()
	if len(checks) != 1 {
		t.Fatalf("recorded checks = %d, want 1", len(checks))
	}
	check := checks[0]
	if check.Status != gopact.VerificationStatusSkipped {
		t.Fatalf("check status = %q, want skipped", check.Status)
	}
	assertMemoryWorkMetadata(t, check.Metadata, "skipped", 0, 0)
}

func deferredMemoryWorkExport(effects []gopact.EffectRecord) gopact.RunExport {
	return gopact.RunExport{
		Version: gopact.RunExportVersion,
		IDs:     gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1", UserID: "user-1"},
		Outcome: gopact.RunCompleted,
		Steps: []gopact.StepSnapshot{
			{
				ID:      "step-1",
				Step:    1,
				Node:    nodeCallModel,
				Phase:   gopact.StepCompleted,
				Effects: effects,
			},
		},
	}
}

func assertMemoryWorkMetadata(t *testing.T, metadata map[string]any, status string, replayCount int, resultCount int) {
	t.Helper()
	if metadata["worker_status"] != status {
		t.Fatalf("metadata worker_status = %v, want %q", metadata["worker_status"], status)
	}
	if metadata["replay_count"] != replayCount {
		t.Fatalf("metadata replay_count = %v, want %d", metadata["replay_count"], replayCount)
	}
	if metadata["result_count"] != resultCount {
		t.Fatalf("metadata result_count = %v, want %d", metadata["result_count"], resultCount)
	}
	if metadata["run_id"] != "run-1" {
		t.Fatalf("metadata run_id = %v, want run-1", metadata["run_id"])
	}
}

func pendingMemoryPutEffect(id string, key string, content string) gopact.EffectRecord {
	return gopact.EffectRecord{
		ID:             id,
		Type:           memory.EffectTypeMemoryPut,
		ReplayPolicy:   gopact.EffectReplayIdempotent,
		IdempotencyKey: key,
		Metadata: map[string]any{
			memory.EffectMetadataMemory: memory.Memory{
				ID:      memory.ID(id),
				Type:    memory.TypeProfile,
				Content: content,
				Scope:   memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
			},
			memoryMetadataPending: true,
		},
	}
}
