package react

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gopact-ai/gopact"
)

var (
	ErrDeferredMemoryWorkScheduleDecisionRequired = errors.New("react: deferred memory work schedule decision is required")
	ErrDeferredMemoryWorkScheduleAttemptRequired  = errors.New("react: deferred memory work schedule attempt is required")
	ErrDeferredMemoryWorkScheduleFailed           = errors.New("react: deferred memory work schedule failed")
	ErrDeferredMemoryWorkDeadLettered             = errors.New("react: deferred memory work dead-lettered")
)

const (
	// VerificationCheckDeferredMemoryWorkSchedule is the standard check ID prefix for host memory worker scheduling decisions.
	VerificationCheckDeferredMemoryWorkSchedule = "react-deferred-memory-work-schedule"

	// VerificationEvidenceTypeDeferredMemoryWorkSchedule is the evidence type for host memory worker scheduling decisions.
	VerificationEvidenceTypeDeferredMemoryWorkSchedule = "memory_work_schedule"
)

// DeferredMemoryWorkScheduleAction describes what the host scheduler decided after one memory worker pass.
type DeferredMemoryWorkScheduleAction string

const (
	// DeferredMemoryWorkScheduleStop means the host scheduler intentionally stopped scheduling more attempts.
	DeferredMemoryWorkScheduleStop DeferredMemoryWorkScheduleAction = "stop"
	// DeferredMemoryWorkScheduleRetry means the host scheduler queued or will queue another attempt.
	DeferredMemoryWorkScheduleRetry DeferredMemoryWorkScheduleAction = "retry"
	// DeferredMemoryWorkScheduleDeadLetter means the host scheduler moved the work to a dead-letter path.
	DeferredMemoryWorkScheduleDeadLetter DeferredMemoryWorkScheduleAction = "dead_letter"
)

// DeferredMemoryWorkScheduleDecision is an already-observed host scheduling decision.
//
// It does not execute retries, own queues, or implement DLQ behavior. Those
// remain host or adapter responsibilities; this type only makes their decisions
// visible to verification reports and release gates.
type DeferredMemoryWorkScheduleDecision struct {
	Report      DeferredMemoryWorkReport
	Action      DeferredMemoryWorkScheduleAction
	Attempt     int
	NextAttempt int
	MaxAttempts int
	Delay       time.Duration
	Reason      string
	Metadata    map[string]any
}

// RecordDeferredMemoryWorkScheduleCheck records an already-observed host memory worker scheduling decision.
func RecordDeferredMemoryWorkScheduleCheck(recorder *gopact.VerificationRecorder, decision DeferredMemoryWorkScheduleDecision) error {
	if recorder == nil {
		return errors.New("react: verification recorder is nil")
	}
	if err := validateDeferredMemoryWorkScheduleDecision(decision); err != nil {
		return err
	}

	check := deferredMemoryWorkScheduleCheck(decision)
	if err := recorder.Record(check); err != nil {
		return err
	}
	if check.Status == gopact.VerificationStatusFailed {
		if decision.Action != DeferredMemoryWorkScheduleDeadLetter {
			return ErrDeferredMemoryWorkScheduleFailed
		}
		return ErrDeferredMemoryWorkDeadLettered
	}
	return nil
}

func validateDeferredMemoryWorkScheduleDecision(decision DeferredMemoryWorkScheduleDecision) error {
	if !decision.Action.valid() {
		return ErrDeferredMemoryWorkScheduleDecisionRequired
	}
	if decision.Attempt <= 0 {
		return ErrDeferredMemoryWorkScheduleAttemptRequired
	}
	return nil
}

func (a DeferredMemoryWorkScheduleAction) valid() bool {
	switch a {
	case DeferredMemoryWorkScheduleStop,
		DeferredMemoryWorkScheduleRetry,
		DeferredMemoryWorkScheduleDeadLetter:
		return true
	default:
		return false
	}
}

func deferredMemoryWorkScheduleCheck(decision DeferredMemoryWorkScheduleDecision) gopact.VerificationCheck {
	status := deferredMemoryWorkScheduleStatus(decision)
	return gopact.VerificationCheck{
		ID:      VerificationCheckDeferredMemoryWorkSchedule + ":" + deferredMemoryWorkScheduleRef(decision),
		Name:    "react deferred memory work schedule",
		Status:  status,
		Summary: deferredMemoryWorkScheduleSummary(status, decision),
		Evidence: []gopact.VerificationEvidence{
			{
				Type:     VerificationEvidenceTypeDeferredMemoryWorkSchedule,
				Ref:      deferredMemoryWorkScheduleRef(decision),
				Summary:  deferredMemoryWorkScheduleEvidenceSummary(decision),
				Metadata: deferredMemoryWorkScheduleCheckMetadata(decision),
			},
		},
		Metadata: deferredMemoryWorkScheduleCheckMetadata(decision),
	}
}

func deferredMemoryWorkScheduleStatus(decision DeferredMemoryWorkScheduleDecision) gopact.VerificationStatus {
	if decision.Action == DeferredMemoryWorkScheduleDeadLetter {
		return gopact.VerificationStatusFailed
	}
	if decision.Action == DeferredMemoryWorkScheduleStop && decision.Report.Status == DeferredMemoryWorkSkipped {
		return gopact.VerificationStatusSkipped
	}
	if decision.Action == DeferredMemoryWorkScheduleStop && decision.Report.Status == DeferredMemoryWorkFailed {
		return gopact.VerificationStatusFailed
	}
	return gopact.VerificationStatusPassed
}

func deferredMemoryWorkScheduleSummary(status gopact.VerificationStatus, decision DeferredMemoryWorkScheduleDecision) string {
	switch status {
	case gopact.VerificationStatusFailed:
		if decision.Action == DeferredMemoryWorkScheduleDeadLetter {
			return "deferred memory work dead-lettered"
		}
		return "deferred memory work scheduling stopped after failure"
	case gopact.VerificationStatusSkipped:
		return "deferred memory work scheduling skipped"
	default:
		if decision.Action == DeferredMemoryWorkScheduleRetry && decision.NextAttempt > 0 {
			return fmt.Sprintf("deferred memory work retry scheduled for attempt %d", decision.NextAttempt)
		}
		return "deferred memory work schedule decision recorded"
	}
}

func deferredMemoryWorkScheduleEvidenceSummary(decision DeferredMemoryWorkScheduleDecision) string {
	if decision.Reason != "" {
		return decision.Reason
	}
	return string(decision.Action)
}

func deferredMemoryWorkScheduleCheckMetadata(decision DeferredMemoryWorkScheduleDecision) map[string]any {
	metadata := deferredMemoryWorkScheduleBaseMetadata(decision)
	if keys := deferredMemoryWorkScheduleSupplementalMetadataKeys(decision.Metadata); len(keys) > 0 {
		metadata["metadata_keys"] = keys
	}
	mergeDeferredMemoryWorkScheduleMetadata(metadata, decision.Metadata)
	return metadata
}

func deferredMemoryWorkScheduleBaseMetadata(decision DeferredMemoryWorkScheduleDecision) map[string]any {
	report := decision.Report
	metadata := map[string]any{
		"ref":           deferredMemoryWorkScheduleRef(decision),
		"action":        string(decision.Action),
		"attempt":       decision.Attempt,
		"worker_status": string(report.Status),
	}
	if decision.NextAttempt > 0 {
		metadata["next_attempt"] = decision.NextAttempt
	}
	if decision.MaxAttempts > 0 {
		metadata["max_attempts"] = decision.MaxAttempts
	}
	if decision.Delay > 0 {
		metadata["delay_ms"] = decision.Delay.Milliseconds()
	}
	if decision.Reason != "" {
		metadata["reason"] = decision.Reason
	}
	if replayCount := deferredMemoryWorkScheduleReplayCount(report); replayCount > 0 {
		metadata["report_replay_count"] = replayCount
	}
	if resultCount := deferredMemoryWorkScheduleResultCount(report); resultCount > 0 {
		metadata["report_result_count"] = resultCount
	}
	if runID := deferredMemoryWorkScheduleRunID(report); runID != "" {
		metadata["run_id"] = runID
	}
	if threadID := deferredMemoryWorkScheduleThreadID(report); threadID != "" {
		metadata["thread_id"] = threadID
	}
	if report.Error != "" {
		metadata["error"] = report.Error
	}
	if ids := deferredMemoryWorkSchedulePlanEffectIDs(report.Plan); len(ids) > 0 {
		metadata["planned_effect_ids"] = ids
	}
	if ids := deferredMemoryWorkScheduleResultEffectIDs(report.Results); len(ids) > 0 {
		metadata["result_effect_ids"] = ids
	}
	if ids := deferredMemoryWorkSchedulePlanStepIDs(report.Plan); len(ids) > 0 {
		metadata["planned_step_ids"] = ids
	}
	if ids := deferredMemoryWorkScheduleResultStepIDs(report.Results); len(ids) > 0 {
		metadata["result_step_ids"] = ids
	}
	return metadata
}

func mergeDeferredMemoryWorkScheduleMetadata(metadata map[string]any, supplemental map[string]any) {
	for key, value := range supplemental {
		if deferredMemoryWorkScheduleReservedMetadataKey(key) {
			continue
		}
		metadata[key] = value
	}
}

func deferredMemoryWorkScheduleSupplementalMetadataKeys(supplemental map[string]any) []string {
	if len(supplemental) == 0 {
		return nil
	}
	keys := make([]string, 0, len(supplemental))
	for key := range supplemental {
		if deferredMemoryWorkScheduleReservedMetadataKey(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func deferredMemoryWorkScheduleReservedMetadataKey(key string) bool {
	switch key {
	case "ref",
		"action",
		"attempt",
		"next_attempt",
		"max_attempts",
		"delay_ms",
		"reason",
		"worker_status",
		"report_replay_count",
		"report_result_count",
		"run_id",
		"thread_id",
		"error",
		"planned_effect_ids",
		"result_effect_ids",
		"planned_step_ids",
		"result_step_ids",
		"metadata_keys":
		return true
	default:
		return false
	}
}

func deferredMemoryWorkScheduleRef(decision DeferredMemoryWorkScheduleDecision) string {
	return fmt.Sprintf("%s:attempt-%d", deferredMemoryWorkReportRef(decision.Report), decision.Attempt)
}

func deferredMemoryWorkScheduleReplayCount(report DeferredMemoryWorkReport) int {
	if report.ReplayCount > 0 {
		return report.ReplayCount
	}
	return report.Plan.ReplayCount
}

func deferredMemoryWorkScheduleResultCount(report DeferredMemoryWorkReport) int {
	if report.ResultCount > 0 {
		return report.ResultCount
	}
	return len(report.Results)
}

func deferredMemoryWorkScheduleRunID(report DeferredMemoryWorkReport) string {
	if report.RunID != "" {
		return report.RunID
	}
	return report.Plan.RunID
}

func deferredMemoryWorkScheduleThreadID(report DeferredMemoryWorkReport) string {
	if report.ThreadID != "" {
		return report.ThreadID
	}
	return report.Plan.ThreadID
}

func deferredMemoryWorkSchedulePlanEffectIDs(plan gopact.RunEffectReplayPlan) []string {
	if len(plan.Decisions) == 0 {
		return nil
	}
	ids := make([]string, 0, len(plan.Decisions))
	for _, decision := range plan.Decisions {
		if id := decision.Decision.Effect.ID; id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func deferredMemoryWorkScheduleResultEffectIDs(results []gopact.RunEffectReplayResult) []string {
	if len(results) == 0 {
		return nil
	}
	ids := make([]string, 0, len(results))
	for _, result := range results {
		if result.Result.EffectID != "" {
			ids = append(ids, result.Result.EffectID)
		}
	}
	return ids
}

func deferredMemoryWorkSchedulePlanStepIDs(plan gopact.RunEffectReplayPlan) []string {
	if len(plan.Decisions) == 0 {
		return nil
	}
	ids := make([]string, 0, len(plan.Decisions))
	seen := make(map[string]struct{}, len(plan.Decisions))
	for _, decision := range plan.Decisions {
		ids = appendDeferredMemoryWorkScheduleStepID(ids, seen, decision.StepID)
	}
	return ids
}

func deferredMemoryWorkScheduleResultStepIDs(results []gopact.RunEffectReplayResult) []string {
	if len(results) == 0 {
		return nil
	}
	ids := make([]string, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		ids = appendDeferredMemoryWorkScheduleStepID(ids, seen, result.StepID)
	}
	return ids
}

func appendDeferredMemoryWorkScheduleStepID(values []string, seen map[string]struct{}, value string) []string {
	if value == "" {
		return values
	}
	if _, ok := seen[value]; ok {
		return values
	}
	seen[value] = struct{}{}
	return append(values, value)
}
