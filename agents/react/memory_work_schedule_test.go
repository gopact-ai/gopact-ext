package react

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
)

func TestRecordDeferredMemoryWorkScheduleCheckRecordsRetryDecision(t *testing.T) {
	report := failedDeferredMemoryWorkReport(t)

	recorder := gopact.NewVerificationRecorder()
	err := RecordDeferredMemoryWorkScheduleCheck(recorder, DeferredMemoryWorkScheduleDecision{
		Report:      report,
		Action:      DeferredMemoryWorkScheduleRetry,
		Attempt:     2,
		NextAttempt: 3,
		MaxAttempts: 5,
		Delay:       250 * time.Millisecond,
		Reason:      "temporary memory store outage",
		Metadata: map[string]any{
			"queue":               "memory-default",
			"action":              string(DeferredMemoryWorkScheduleDeadLetter),
			"attempt":             99,
			"next_attempt":        99,
			"max_attempts":        99,
			"delay_ms":            99,
			"worker_status":       "forged",
			"report_replay_count": 99,
			"report_result_count": 99,
			"run_id":              "forged-run",
			"thread_id":           "forged-thread",
			"error":               "forged-error",
			"planned_step_ids":    []string{"forged-step"},
			"result_step_ids":     []string{"forged-result-step"},
			"metadata_keys":       []string{"forged"},
			"scope":               "release",
			"source":              "scheduler",
		},
	})
	if err != nil {
		t.Fatalf("RecordDeferredMemoryWorkScheduleCheck() error = %v", err)
	}

	checks := recorder.Checks()
	if len(checks) != 1 {
		t.Fatalf("recorded checks = %d, want 1", len(checks))
	}
	check := checks[0]
	if check.ID != VerificationCheckDeferredMemoryWorkSchedule+":run-1:attempt-2" {
		t.Fatalf("check ID = %q, want schedule decision check", check.ID)
	}
	if check.Status != gopact.VerificationStatusPassed {
		t.Fatalf("check status = %q, want passed", check.Status)
	}
	if len(check.Evidence) != 1 || check.Evidence[0].Type != VerificationEvidenceTypeDeferredMemoryWorkSchedule {
		t.Fatalf("check evidence = %+v, want memory work schedule evidence", check.Evidence)
	}
	metadata := check.Metadata
	if metadata["action"] != string(DeferredMemoryWorkScheduleRetry) ||
		metadata["attempt"] != 2 ||
		metadata["next_attempt"] != 3 ||
		metadata["max_attempts"] != 5 ||
		metadata["delay_ms"] != int64(250) ||
		metadata["worker_status"] != string(DeferredMemoryWorkFailed) ||
		metadata["report_replay_count"] != 2 ||
		metadata["report_result_count"] != 1 ||
		metadata["run_id"] != "run-1" ||
		metadata["thread_id"] != "thread-1" ||
		metadata["error"] != report.Error {
		t.Fatalf("metadata = %+v, want canonical schedule fields preserved", metadata)
	}
	if metadata["queue"] != "memory-default" {
		t.Fatalf("metadata = %+v, want supplemental queue metadata", metadata)
	}
	if metadata["scope"] != "release" || metadata["source"] != "scheduler" {
		t.Fatalf("metadata = %+v, want supplemental metadata preserved", metadata)
	}
	if keys, ok := metadata["metadata_keys"].([]string); !ok ||
		!reflect.DeepEqual(keys, []string{"queue", "scope", "source"}) {
		t.Fatalf("metadata keys = %#v, want supplemental metadata key summary", metadata["metadata_keys"])
	}
	if planned, ok := metadata["planned_step_ids"].([]string); !ok ||
		!reflect.DeepEqual(planned, []string{"step-1"}) {
		t.Fatalf("metadata planned step ids = %#v, want canonical planned step ids", metadata["planned_step_ids"])
	}
	if results, ok := metadata["result_step_ids"].([]string); !ok ||
		!reflect.DeepEqual(results, []string{"step-1"}) {
		t.Fatalf("metadata result step ids = %#v, want canonical result step ids", metadata["result_step_ids"])
	}

	evidenceMetadata := check.Evidence[0].Metadata
	if evidenceMetadata["action"] != string(DeferredMemoryWorkScheduleRetry) ||
		evidenceMetadata["attempt"] != 2 ||
		evidenceMetadata["next_attempt"] != 3 ||
		evidenceMetadata["max_attempts"] != 5 ||
		evidenceMetadata["delay_ms"] != int64(250) ||
		evidenceMetadata["worker_status"] != string(DeferredMemoryWorkFailed) ||
		evidenceMetadata["report_replay_count"] != 2 ||
		evidenceMetadata["report_result_count"] != 1 ||
		evidenceMetadata["run_id"] != "run-1" ||
		evidenceMetadata["thread_id"] != "thread-1" ||
		evidenceMetadata["error"] != report.Error {
		t.Fatalf("evidence metadata = %+v, want canonical schedule fields preserved", evidenceMetadata)
	}
	if planned, ok := evidenceMetadata["planned_effect_ids"].([]string); !ok ||
		!reflect.DeepEqual(planned, []string{"pending-1", "pending-2"}) {
		t.Fatalf("evidence planned effect ids = %#v, want canonical planned ids", evidenceMetadata["planned_effect_ids"])
	}
	if results, ok := evidenceMetadata["result_effect_ids"].([]string); !ok ||
		!reflect.DeepEqual(results, []string{"pending-1"}) {
		t.Fatalf("evidence result effect ids = %#v, want canonical result ids", evidenceMetadata["result_effect_ids"])
	}
	if planned, ok := evidenceMetadata["planned_step_ids"].([]string); !ok ||
		!reflect.DeepEqual(planned, []string{"step-1"}) {
		t.Fatalf("evidence planned step ids = %#v, want canonical planned step ids", evidenceMetadata["planned_step_ids"])
	}
	if results, ok := evidenceMetadata["result_step_ids"].([]string); !ok ||
		!reflect.DeepEqual(results, []string{"step-1"}) {
		t.Fatalf("evidence result step ids = %#v, want canonical result step ids", evidenceMetadata["result_step_ids"])
	}
	if evidenceMetadata["queue"] != "memory-default" {
		t.Fatalf("evidence metadata = %+v, want supplemental queue metadata", evidenceMetadata)
	}
	if evidenceMetadata["scope"] != "release" || evidenceMetadata["source"] != "scheduler" {
		t.Fatalf("evidence metadata = %+v, want supplemental metadata preserved", evidenceMetadata)
	}
	if keys, ok := evidenceMetadata["metadata_keys"].([]string); !ok ||
		!reflect.DeepEqual(keys, []string{"queue", "scope", "source"}) {
		t.Fatalf("evidence metadata keys = %#v, want supplemental metadata key summary", evidenceMetadata["metadata_keys"])
	}
}

func TestRecordDeferredMemoryWorkScheduleCheckRecordsDeadLetterAsFailure(t *testing.T) {
	report := failedDeferredMemoryWorkReport(t)

	recorder := gopact.NewVerificationRecorder()
	err := RecordDeferredMemoryWorkScheduleCheck(recorder, DeferredMemoryWorkScheduleDecision{
		Report:      report,
		Action:      DeferredMemoryWorkScheduleDeadLetter,
		Attempt:     3,
		MaxAttempts: 3,
		Reason:      "max attempts reached",
		Metadata:    map[string]any{"queue": "memory-dead-letter"},
	})
	if !errors.Is(err, ErrDeferredMemoryWorkDeadLettered) {
		t.Fatalf("RecordDeferredMemoryWorkScheduleCheck() error = %v, want ErrDeferredMemoryWorkDeadLettered", err)
	}

	checks := recorder.Checks()
	if len(checks) != 1 {
		t.Fatalf("recorded checks = %d, want 1", len(checks))
	}
	check := checks[0]
	if check.Status != gopact.VerificationStatusFailed {
		t.Fatalf("check status = %q, want failed", check.Status)
	}
	if check.Metadata["action"] != string(DeferredMemoryWorkScheduleDeadLetter) ||
		check.Metadata["queue"] != "memory-dead-letter" {
		t.Fatalf("check metadata = %+v, want dead-letter action and queue metadata", check.Metadata)
	}
}

func TestRecordDeferredMemoryWorkScheduleCheckRecordsStopAfterFailureAsGenericFailure(t *testing.T) {
	report := failedDeferredMemoryWorkReport(t)

	recorder := gopact.NewVerificationRecorder()
	err := RecordDeferredMemoryWorkScheduleCheck(recorder, DeferredMemoryWorkScheduleDecision{
		Report:  report,
		Action:  DeferredMemoryWorkScheduleStop,
		Attempt: 2,
		Reason:  "operator stopped retries",
	})
	if !errors.Is(err, ErrDeferredMemoryWorkScheduleFailed) {
		t.Fatalf("RecordDeferredMemoryWorkScheduleCheck() error = %v, want ErrDeferredMemoryWorkScheduleFailed", err)
	}
	if errors.Is(err, ErrDeferredMemoryWorkDeadLettered) {
		t.Fatalf("RecordDeferredMemoryWorkScheduleCheck() error = %v, should not report dead-letter for stop", err)
	}

	checks := recorder.Checks()
	if len(checks) != 1 || checks[0].Status != gopact.VerificationStatusFailed {
		t.Fatalf("checks = %+v, want one failed stop check", checks)
	}
}

func TestRecordDeferredMemoryWorkScheduleCheckRejectsInvalidInput(t *testing.T) {
	recorder := gopact.NewVerificationRecorder()
	report := failedDeferredMemoryWorkReport(t)

	if err := RecordDeferredMemoryWorkScheduleCheck(nil, DeferredMemoryWorkScheduleDecision{
		Report:  report,
		Action:  DeferredMemoryWorkScheduleRetry,
		Attempt: 1,
	}); err == nil {
		t.Fatal("RecordDeferredMemoryWorkScheduleCheck(nil) error = nil, want error")
	}
	if err := RecordDeferredMemoryWorkScheduleCheck(recorder, DeferredMemoryWorkScheduleDecision{
		Report:  report,
		Attempt: 1,
	}); !errors.Is(err, ErrDeferredMemoryWorkScheduleDecisionRequired) {
		t.Fatalf("RecordDeferredMemoryWorkScheduleCheck(missing action) error = %v, want ErrDeferredMemoryWorkScheduleDecisionRequired", err)
	}
	if err := RecordDeferredMemoryWorkScheduleCheck(recorder, DeferredMemoryWorkScheduleDecision{
		Report: report,
		Action: DeferredMemoryWorkScheduleRetry,
	}); !errors.Is(err, ErrDeferredMemoryWorkScheduleAttemptRequired) {
		t.Fatalf("RecordDeferredMemoryWorkScheduleCheck(missing attempt) error = %v, want ErrDeferredMemoryWorkScheduleAttemptRequired", err)
	}
	if len(recorder.Checks()) != 0 {
		t.Fatalf("checks = %+v, want no checks for invalid input", recorder.Checks())
	}
}

func failedDeferredMemoryWorkReport(t *testing.T) DeferredMemoryWorkReport {
	t.Helper()

	replayErr := errors.New("worker executor failed")
	export := deferredMemoryWorkExport([]gopact.EffectRecord{
		pendingMemoryPutEffect("pending-1", "memory:pending-1", "first memory"),
		pendingMemoryPutEffect("pending-2", "memory:pending-2", "second memory"),
	})
	executor := gopact.EffectReplayFunc(func(_ context.Context, decision gopact.EffectReplayDecision) (gopact.EffectReplayResult, error) {
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
	report, err := RunDeferredMemoryWork(context.Background(), export, executor)
	if !errors.Is(err, replayErr) {
		t.Fatalf("RunDeferredMemoryWork() error = %v, want replayErr", err)
	}
	return report
}
