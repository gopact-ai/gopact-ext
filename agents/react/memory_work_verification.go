package react

import (
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/memory"
)

const (
	// VerificationCheckDeferredMemoryWork is the standard check ID prefix for ReAct deferred memory worker reports.
	VerificationCheckDeferredMemoryWork = "react-deferred-memory-work"
)

// RecordDeferredMemoryWorkCheck records an already-observed deferred memory worker pass.
func RecordDeferredMemoryWorkCheck(recorder *gopact.VerificationRecorder, report DeferredMemoryWorkReport) error {
	return memory.RecordReplayCheck(recorder, deferredMemoryWorkReplaySnapshot(report))
}

func deferredMemoryWorkReplaySnapshot(report DeferredMemoryWorkReport) memory.ReplayVerificationSnapshot {
	return memory.ReplayVerificationSnapshot{
		ID:       VerificationCheckDeferredMemoryWork + ":" + deferredMemoryWorkReportRef(report),
		Name:     "react deferred memory work",
		Ref:      deferredMemoryWorkReportRef(report),
		Plan:     report.Plan,
		Results:  report.Results,
		Err:      deferredMemoryWorkReportError(report),
		Skipped:  report.Status == DeferredMemoryWorkSkipped,
		Summary:  deferredMemoryWorkReportSummary(report),
		Metadata: deferredMemoryWorkReportMetadata(report),
	}
}

func deferredMemoryWorkReportRef(report DeferredMemoryWorkReport) string {
	if report.RunID != "" {
		return report.RunID
	}
	if report.ThreadID != "" {
		return report.ThreadID
	}
	if report.Plan.RunID != "" {
		return report.Plan.RunID
	}
	if report.Plan.ThreadID != "" {
		return report.Plan.ThreadID
	}
	return VerificationCheckDeferredMemoryWork
}

func deferredMemoryWorkReportError(report DeferredMemoryWorkReport) error {
	if report.Error == "" {
		return nil
	}
	return errors.New(report.Error)
}

func deferredMemoryWorkReportSummary(report DeferredMemoryWorkReport) string {
	switch report.Status {
	case DeferredMemoryWorkSkipped:
		return "deferred memory work skipped"
	case DeferredMemoryWorkFailed:
		if report.Error != "" {
			return "deferred memory work failed: " + report.Error
		}
		return "deferred memory work failed"
	default:
		if report.ReplayCount == 1 {
			return "deferred memory work completed with 1 effect"
		}
		return fmt.Sprintf("deferred memory work completed with %d effects", report.ReplayCount)
	}
}

func deferredMemoryWorkReportMetadata(report DeferredMemoryWorkReport) map[string]any {
	metadata := map[string]any{
		"worker_status": string(report.Status),
	}
	if report.ReplayCount > 0 {
		metadata["report_replay_count"] = report.ReplayCount
	}
	if report.ResultCount > 0 {
		metadata["report_result_count"] = report.ResultCount
	}
	if report.RunID != "" {
		metadata["run_id"] = report.RunID
	}
	if report.ThreadID != "" {
		metadata["thread_id"] = report.ThreadID
	}
	return metadata
}
