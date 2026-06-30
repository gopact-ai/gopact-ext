package react

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/memory"
)

var ErrMemoryWorkExecutorRequired = errors.New("react: memory work executor is required")

// DeferredMemoryWorkStatus is the observable outcome of one host-managed memory worker pass.
type DeferredMemoryWorkStatus string

const (
	// DeferredMemoryWorkSkipped means the run export did not contain pending memory work.
	DeferredMemoryWorkSkipped DeferredMemoryWorkStatus = "skipped"
	// DeferredMemoryWorkSucceeded means all selected deferred memory effects replayed successfully.
	DeferredMemoryWorkSucceeded DeferredMemoryWorkStatus = "succeeded"
	// DeferredMemoryWorkFailed means planning or replay failed. Results may contain partial progress.
	DeferredMemoryWorkFailed DeferredMemoryWorkStatus = "failed"
)

// DeferredMemoryWorkReport is the SDK-level worker contract for host-managed memory jobs.
//
// It intentionally reports one worker pass only. Production queueing,
// concurrency, retry policy, and dead-letter handling remain the host or
// adapter's responsibility.
type DeferredMemoryWorkReport struct {
	RunID       string                         `json:"run_id,omitempty"`
	ThreadID    string                         `json:"thread_id,omitempty"`
	Status      DeferredMemoryWorkStatus       `json:"status"`
	Plan        gopact.RunEffectReplayPlan     `json:"plan"`
	Results     []gopact.RunEffectReplayResult `json:"results,omitempty"`
	ReplayCount int                            `json:"replay_count,omitempty"`
	ResultCount int                            `json:"result_count,omitempty"`
	Error       string                         `json:"error,omitempty"`
}

// PlanDeferredMemoryWork extracts pending deferred memory effects from a run export.
//
// The returned plan is intentionally only a scheduling contract. Production
// queueing, concurrency, retries, and dead-letter handling belong to the host
// worker or adapter, not to the ReAct template.
func PlanDeferredMemoryWork(export gopact.RunExport) (gopact.RunEffectReplayPlan, error) {
	plan, err := gopact.PlanRunEffectReplay(export)
	if err != nil {
		return gopact.RunEffectReplayPlan{}, fmt.Errorf("react: plan deferred memory work: %w", err)
	}
	return filterDeferredMemoryWork(plan), nil
}

// RunDeferredMemoryWork plans and executes one host-managed deferred memory worker pass.
func RunDeferredMemoryWork(ctx context.Context, export gopact.RunExport, executor gopact.EffectReplayExecutor) (DeferredMemoryWorkReport, error) {
	plan, err := PlanDeferredMemoryWork(export)
	report := DeferredMemoryWorkReport{
		RunID:    export.IDs.RunID,
		ThreadID: export.IDs.ThreadID,
		Status:   DeferredMemoryWorkFailed,
	}
	if err != nil {
		err = fmt.Errorf("react: run deferred memory work: %w", err)
		report.Error = err.Error()
		return report, err
	}

	report.RunID = plan.RunID
	report.ThreadID = plan.ThreadID
	report.Plan = plan
	report.ReplayCount = plan.ReplayCount
	if len(plan.Decisions) == 0 {
		report.Status = DeferredMemoryWorkSkipped
		return report, nil
	}
	if executor == nil {
		report.Error = ErrMemoryWorkExecutorRequired.Error()
		return report, ErrMemoryWorkExecutorRequired
	}

	results, err := gopact.ExecuteRunEffectReplay(ctx, plan, executor)
	report.Results = results
	report.ResultCount = len(results)
	if err != nil {
		err = fmt.Errorf("react: run deferred memory work: %w", err)
		report.Error = err.Error()
		return report, err
	}
	report.Status = DeferredMemoryWorkSucceeded
	return report, nil
}

// ExecuteDeferredMemoryWork replays the deferred memory work selected by PlanDeferredMemoryWork.
func ExecuteDeferredMemoryWork(ctx context.Context, plan gopact.RunEffectReplayPlan, executor gopact.EffectReplayExecutor) ([]gopact.RunEffectReplayResult, error) {
	if ctx == nil {
		ctx = context.TODO()
	}
	work := filterDeferredMemoryWork(plan)
	if len(work.Decisions) == 0 {
		return nil, nil
	}
	if executor == nil {
		return nil, ErrMemoryWorkExecutorRequired
	}
	results, err := gopact.ExecuteRunEffectReplay(ctx, work, executor)
	if err != nil {
		return nil, fmt.Errorf("react: execute deferred memory work: %w", err)
	}
	return results, nil
}

func filterDeferredMemoryWork(plan gopact.RunEffectReplayPlan) gopact.RunEffectReplayPlan {
	out := gopact.RunEffectReplayPlan{
		RunID:    plan.RunID,
		ThreadID: plan.ThreadID,
	}
	for _, decision := range plan.Decisions {
		if !isDeferredMemoryWorkDecision(decision.Decision) {
			continue
		}
		out.Decisions = append(out.Decisions, copyRunEffectReplayDecision(decision))
		out.ReplayCount++
	}
	return out
}

func isDeferredMemoryWorkDecision(decision gopact.EffectReplayDecision) bool {
	if decision.Action != gopact.EffectReplayActionReplay {
		return false
	}
	if decision.Effect.Applied {
		return false
	}
	if !isDeferredMemoryEffectType(decision.Effect.Type) {
		return false
	}
	pending, _ := decision.Effect.Metadata[memoryMetadataPending].(bool)
	return pending
}

func isDeferredMemoryEffectType(effectType string) bool {
	switch effectType {
	case memory.EffectTypeMemoryPut, memory.EffectTypeMemoryExtract:
		return true
	default:
		return false
	}
}

func copyRunEffectReplayDecision(in gopact.RunEffectReplayDecision) gopact.RunEffectReplayDecision {
	out := in
	out.Decision = copyEffectReplayDecision(in.Decision)
	return out
}

func copyEffectReplayDecision(in gopact.EffectReplayDecision) gopact.EffectReplayDecision {
	out := in
	out.Effect = copySingleEffectRecord(in.Effect)
	return out
}

func copySingleEffectRecord(in gopact.EffectRecord) gopact.EffectRecord {
	records := copyEffectRecords([]gopact.EffectRecord{in})
	if len(records) == 0 {
		return gopact.EffectRecord{}
	}
	return records[0]
}
