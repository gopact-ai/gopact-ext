package selfbootstrap

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestWorkflowRunProducesSelfBootstrapEvidence(t *testing.T) {
	workflow, err := New(
		WithAnalyzer(AnalyzerFunc(func(context.Context, Request) (Analysis, error) {
			return Analysis{Summary: "scope is small and testable"}, nil
		})),
		WithPlanner(PlannerFunc(func(context.Context, PlanRequest) (Plan, error) {
			return Plan{
				Summary: "ship one tested slice",
				Steps: []PlanStep{
					{ID: "test", Summary: "lock behavior with tests"},
					{ID: "impl", Summary: "implement workflow"},
				},
			}, nil
		})),
		WithWriter(WriterFunc(func(context.Context, WriteRequest) (WriteResult, error) {
			return WriteResult{
				Summary: "self-bootstrap workflow added",
				Diff: &gopacttest.DiffSnapshot{
					ID:         "diff:worktree",
					Ref:        "git:worktree",
					Diff:       "diff --git a/devagent/selfbootstrap/selfbootstrap.go b/devagent/selfbootstrap/selfbootstrap.go\n",
					Files:      []string{"devagent/selfbootstrap/selfbootstrap.go"},
					Insertions: 48,
				},
				FileSnapshots: []gopacttest.FileSnapshot{
					{
						ID:            "file-snapshot:devagent/selfbootstrap/go.mod",
						Path:          "devagent/selfbootstrap/go.mod",
						Hash:          "abc123",
						HashAlgorithm: "sha256",
						SizeBytes:     128,
					},
				},
			}, nil
		})),
		WithTester(TesterFunc(func(context.Context, TestRequest) (TestResult, error) {
			return TestResult{
				RequiredGates: []string{gopacttest.SelfBootstrapCIGateUnit},
				Commands: []gopacttest.CommandResult{
					{
						ID:       "command:go test -count=1 ./devagent/selfbootstrap",
						Command:  []string{"go", "test", "-count=1", "./..."},
						ExitCode: 0,
					},
				},
				Gates: []gopacttest.CIGateResult{
					{
						Gate: gopacttest.SelfBootstrapCIGateUnit,
						Result: gopacttest.CommandResult{
							Command:  []string{"go", "test", "-count=1", "./..."},
							ExitCode: 0,
						},
					},
				},
			}, nil
		})),
		WithReviewer(ReviewerFunc(func(context.Context, ReviewRequest) (gopacttest.ReviewResult, error) {
			return gopacttest.ReviewResult{
				ID:       "review:selfbootstrap",
				Reviewer: "ci-reviewer",
				Source:   "mock",
				Status:   gopacttest.ReviewStatusApproved,
				Summary:  "approved",
			}, nil
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := workflow.Run(context.Background(), Request{
		Objective:  "add a reusable self-bootstrap workflow",
		Repository: "gopact-ext",
		IDs: gopact.RuntimeIDs{
			RunID:    "selfbootstrap-success",
			ThreadID: "thread-1",
			AgentID:  "devagent-selfbootstrap",
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.RunExport.Outcome != gopact.RunCompleted {
		t.Fatalf("RunExport.Outcome = %q, want %q", result.RunExport.Outcome, gopact.RunCompleted)
	}
	if result.Report.Status != gopact.VerificationStatusPassed {
		t.Fatalf("Report.Status = %q, want passed; checks=%+v", result.Report.Status, result.Report.Checks)
	}
	if len(result.RunExport.VerificationReports) != 1 {
		t.Fatalf("embedded verification reports = %d, want 1", len(result.RunExport.VerificationReports))
	}
	requireWorkflowNodes(t, result.RunExport, []string{"analyze", "plan", "write", "test", "review"})
	requireEvidenceTypes(t, result.Report, []string{
		gopact.VerificationEvidenceTypeRunExport,
		gopacttest.VerificationEvidenceTypeCommand,
		gopacttest.VerificationEvidenceTypeCIGate,
		gopacttest.VerificationEvidenceTypeDiff,
		gopacttest.VerificationEvidenceTypeFileSnapshot,
		gopacttest.VerificationEvidenceTypeReview,
	})
}

func TestWorkflowStopsWhenReviewRejects(t *testing.T) {
	workflow := mustWorkflow(t,
		WithReviewer(ReviewerFunc(func(context.Context, ReviewRequest) (gopacttest.ReviewResult, error) {
			return gopacttest.ReviewResult{
				ID:       "review:selfbootstrap",
				Reviewer: "ci-reviewer",
				Source:   "mock",
				Status:   gopacttest.ReviewStatusRejected,
				Summary:  "requires changes",
			}, nil
		})),
	)

	result, err := workflow.Run(context.Background(), defaultRequest("selfbootstrap-review-rejected"))
	if !errors.Is(err, ErrReviewRejected) {
		t.Fatalf("Run() error = %v, want ErrReviewRejected", err)
	}
	if result.RunExport.Outcome != gopact.RunFailed {
		t.Fatalf("RunExport.Outcome = %q, want failed", result.RunExport.Outcome)
	}
	if result.Report.Status != gopact.VerificationStatusFailed {
		t.Fatalf("Report.Status = %q, want failed", result.Report.Status)
	}
	requireFailedCheck(t, result.Report, "review:selfbootstrap")
}

func TestWorkflowAuthorizesPlanPatchBeforeWrite(t *testing.T) {
	var events []string
	workflow := mustWorkflow(t,
		WithPlanner(PlannerFunc(func(context.Context, PlanRequest) (Plan, error) {
			return Plan{
				Summary: "propose one patch",
				Patch: &PatchProposal{
					ID:      "patch-1",
					Summary: "update greeting",
					Diff:    "diff --git a/hello.txt b/hello.txt\n",
					Files:   []PatchFile{{Path: "hello.txt", Intent: "modify"}},
					Metadata: map[string]any{
						"source_step": "plan",
					},
				},
			}, nil
		})),
		WithPatchPolicy(gopact.PolicyFunc(func(_ context.Context, request gopact.PolicyRequest) (gopact.PolicyDecision, error) {
			events = append(events, "policy")
			if request.Boundary != gopact.PolicyBoundarySandbox || request.Action != gopact.PolicyActionWrite {
				t.Fatalf("policy request = %+v, want sandbox write", request)
			}
			input, ok := request.Input.(PatchPolicyInput)
			if !ok {
				t.Fatalf("policy input type = %T, want PatchPolicyInput", request.Input)
			}
			if input.ID != "patch-1" || input.Summary != "update greeting" ||
				!input.HasDiff || input.DiffBytes == 0 ||
				len(input.Files) != 1 || input.Files[0].Path != "hello.txt" {
				t.Fatalf("policy input = %+v, want sanitized patch summary", input)
			}
			return gopact.PolicyDecision{
				Action: gopact.PolicyAllow,
				Reason: "small scoped patch",
				Metadata: map[string]any{
					"reviewer": "unit-policy",
				},
			}, nil
		})),
		WithWriter(WriterFunc(func(_ context.Context, request WriteRequest) (WriteResult, error) {
			events = append(events, "writer")
			if request.Plan.Patch == nil || request.Plan.Patch.ID != "patch-1" {
				t.Fatalf("write request patch = %+v, want patch-1", request.Plan.Patch)
			}
			if request.PatchDecision == nil || request.PatchDecision.Action != gopact.PolicyAllow {
				t.Fatalf("write request patch decision = %+v, want allow", request.PatchDecision)
			}
			return WriteResult{
				Summary: "patch applied",
				Diff: &gopacttest.DiffSnapshot{
					ID:         "diff:worktree",
					Ref:        "git:worktree",
					Diff:       "diff --git a/hello.txt b/hello.txt\n",
					Files:      []string{"hello.txt"},
					Insertions: 1,
				},
			}, nil
		})),
	)

	result, err := workflow.Run(context.Background(), defaultRequest("selfbootstrap-patch-policy-allowed"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"policy", "writer"}) {
		t.Fatalf("events = %+v, want policy before writer", events)
	}
	if result.PatchDecision.Action != gopact.PolicyAllow {
		t.Fatalf("result patch decision = %+v, want allow", result.PatchDecision)
	}
	requireEvidenceTypes(t, result.Report, []string{
		gopact.VerificationEvidenceTypePolicyDecision,
		gopacttest.VerificationEvidenceTypeDiff,
	})
	requireEventTypes(t, result.RunExport, []gopact.EventType{
		gopact.EventPolicyRequested,
		gopact.EventPolicyDecided,
	})
}

func TestWorkflowStopsBeforeWriteWhenPatchPolicyDenies(t *testing.T) {
	writerCalled := false
	workflow := mustWorkflow(t,
		WithPlanner(PlannerFunc(func(context.Context, PlanRequest) (Plan, error) {
			return Plan{
				Summary: "propose risky patch",
				Patch: &PatchProposal{
					ID:      "patch-risky",
					Summary: "modify generated file",
					Diff:    "diff --git a/generated.go b/generated.go\n",
					Files:   []PatchFile{{Path: "generated.go", Intent: "modify"}},
				},
			}, nil
		})),
		WithPatchPolicy(gopact.PolicyFunc(func(context.Context, gopact.PolicyRequest) (gopact.PolicyDecision, error) {
			return gopact.PolicyDecision{Action: gopact.PolicyDeny, Reason: "generated file"}, nil
		})),
		WithWriter(WriterFunc(func(context.Context, WriteRequest) (WriteResult, error) {
			writerCalled = true
			return WriteResult{}, nil
		})),
	)

	result, err := workflow.Run(context.Background(), defaultRequest("selfbootstrap-patch-policy-denied"))
	if !errors.Is(err, gopact.ErrPolicyDenied) {
		t.Fatalf("Run() error = %v, want gopact.ErrPolicyDenied", err)
	}
	if writerCalled {
		t.Fatal("writer was called after patch policy denied")
	}
	if result.RunExport.Outcome != gopact.RunFailed {
		t.Fatalf("RunExport.Outcome = %q, want failed", result.RunExport.Outcome)
	}
	if result.RunExport.Failures[0].Kind != gopact.FailurePolicy {
		t.Fatalf("failure kind = %q, want policy", result.RunExport.Failures[0].Kind)
	}
	requireFailedCheck(t, result.Report, "policy-decision:selfbootstrap-patch-policy-denied:sandbox:write")
}

func TestWorkflowPreservesTestFailureEvidence(t *testing.T) {
	workflow := mustWorkflow(t,
		WithTester(TesterFunc(func(context.Context, TestRequest) (TestResult, error) {
			return TestResult{
				RequiredGates: []string{gopacttest.SelfBootstrapCIGateUnit},
				Commands: []gopacttest.CommandResult{
					{
						ID:       "command:go test -count=1 ./...",
						Command:  []string{"go", "test", "-count=1", "./..."},
						ExitCode: 1,
						Stderr:   "unit test failed",
					},
				},
				Gates: []gopacttest.CIGateResult{
					{
						Gate: gopacttest.SelfBootstrapCIGateUnit,
						Result: gopacttest.CommandResult{
							Command:  []string{"go", "test", "-count=1", "./..."},
							ExitCode: 1,
							Stderr:   "unit test failed",
						},
					},
				},
			}, nil
		})),
	)

	result, err := workflow.Run(context.Background(), defaultRequest("selfbootstrap-test-failed"))
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Run() error = %v, want ErrVerificationFailed", err)
	}
	if result.Report.Status != gopact.VerificationStatusFailed {
		t.Fatalf("Report.Status = %q, want failed", result.Report.Status)
	}
	requireFailedCheck(t, result.Report, "command:go test -count=1 ./...")
	if len(result.RunExport.Failures) == 0 {
		t.Fatalf("RunExport.Failures is empty, want verification failure attribution")
	}
	if result.RunExport.Failures[0].Kind != gopact.FailureVerification {
		t.Fatalf("failure kind = %q, want %q", result.RunExport.Failures[0].Kind, gopact.FailureVerification)
	}
	requireEvidenceTypes(t, result.Report, []string{
		gopacttest.VerificationEvidenceTypeCommand,
		gopacttest.VerificationEvidenceTypeCIGate,
	})
}

func TestWorkflowValidatesConstructionAndRequest(t *testing.T) {
	if _, err := New(); !errors.Is(err, ErrStageRequired) {
		t.Fatalf("New() error = %v, want ErrStageRequired", err)
	}
	if _, err := New(WithAnalyzer(nil)); !errors.Is(err, ErrStageRequired) {
		t.Fatalf("New(WithAnalyzer(nil)) error = %v, want ErrStageRequired", err)
	}
	for name, opt := range map[string]Option{
		"planner":  WithPlanner(nil),
		"writer":   WithWriter(nil),
		"tester":   WithTester(nil),
		"reviewer": WithReviewer(nil),
	} {
		if _, err := New(opt); !errors.Is(err, ErrStageRequired) {
			t.Fatalf("New(With%s(nil)) error = %v, want ErrStageRequired", name, err)
		}
	}

	var nilWorkflow *Workflow
	if _, err := nilWorkflow.Run(context.Background(), defaultRequest("nil-workflow")); !errors.Is(err, ErrWorkflowUnavailable) {
		t.Fatalf("nil workflow Run() error = %v, want ErrWorkflowUnavailable", err)
	}

	workflow := mustWorkflow(t)
	missingObjective := defaultRequest("missing-objective")
	missingObjective.Objective = " "
	if _, err := workflow.Run(context.Background(), missingObjective); !errors.Is(err, ErrObjectiveRequired) {
		t.Fatalf("Run(missing objective) error = %v, want ErrObjectiveRequired", err)
	}
	missingRunID := defaultRequest("")
	if _, err := workflow.Run(context.Background(), missingRunID); !errors.Is(err, ErrRunIDRequired) {
		t.Fatalf("Run(missing run id) error = %v, want ErrRunIDRequired", err)
	}
}

func TestWorkflowReturnsStageErrorsWithFailedRunExport(t *testing.T) {
	stageErr := errors.New("analysis failed")
	workflow := mustWorkflow(t,
		WithAnalyzer(AnalyzerFunc(func(context.Context, Request) (Analysis, error) {
			return Analysis{Summary: "partial analysis"}, stageErr
		})),
	)

	result, err := workflow.Run(context.Background(), defaultRequest("selfbootstrap-analysis-failed"))
	if !errors.Is(err, stageErr) {
		t.Fatalf("Run() error = %v, want analysis failure", err)
	}
	if result.RunExport.Outcome != gopact.RunFailed {
		t.Fatalf("RunExport.Outcome = %q, want failed", result.RunExport.Outcome)
	}
	if result.Report.Status != gopact.VerificationStatusFailed {
		t.Fatalf("Report.Status = %q, want failed", result.Report.Status)
	}
	if len(result.RunExport.Failures) == 0 || result.RunExport.Failures[0].Kind != gopact.FailureRuntime {
		t.Fatalf("Failures = %+v, want runtime failure attribution", result.RunExport.Failures)
	}
}

func TestWorkflowReturnsStageErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		opt      Option
		wantKind gopact.FailureKind
	}{
		{
			name: "planner",
			opt: WithPlanner(PlannerFunc(func(context.Context, PlanRequest) (Plan, error) {
				return Plan{Summary: "partial plan"}, errors.New("planner failed")
			})),
			wantKind: gopact.FailureRuntime,
		},
		{
			name: "writer",
			opt: WithWriter(WriterFunc(func(context.Context, WriteRequest) (WriteResult, error) {
				return WriteResult{Summary: "partial write"}, errors.New("writer failed")
			})),
			wantKind: gopact.FailureRuntime,
		},
		{
			name: "tester",
			opt: WithTester(TesterFunc(func(context.Context, TestRequest) (TestResult, error) {
				return TestResult{Summary: "partial test"}, errors.New("tester failed")
			})),
			wantKind: gopact.FailureRuntime,
		},
		{
			name: "reviewer",
			opt: WithReviewer(ReviewerFunc(func(context.Context, ReviewRequest) (gopacttest.ReviewResult, error) {
				return gopacttest.ReviewResult{}, errors.New("reviewer failed")
			})),
			wantKind: gopact.FailureFeedback,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			workflow := mustWorkflow(t, tt.opt)
			result, err := workflow.Run(context.Background(), defaultRequest("selfbootstrap-"+tt.name+"-failed"))
			if err == nil {
				t.Fatal("Run() error = nil, want stage error")
			}
			if result.RunExport.Outcome != gopact.RunFailed {
				t.Fatalf("RunExport.Outcome = %q, want failed", result.RunExport.Outcome)
			}
			if result.Report.Status != gopact.VerificationStatusFailed {
				t.Fatalf("Report.Status = %q, want failed", result.Report.Status)
			}
			if len(result.RunExport.Failures) == 0 || result.RunExport.Failures[0].Kind != tt.wantKind {
				t.Fatalf("Failures = %+v, want %s attribution", result.RunExport.Failures, tt.wantKind)
			}
		})
	}
}

func TestWorkflowStopsWhenWriteEvidenceFails(t *testing.T) {
	workflow := mustWorkflow(t,
		WithWriter(WriterFunc(func(context.Context, WriteRequest) (WriteResult, error) {
			return WriteResult{
				Summary: "snapshot failed",
				FileSnapshots: []gopacttest.FileSnapshot{
					{ID: "file-snapshot:go.mod", Path: "go.mod", Err: errors.New("read failed")},
				},
			}, nil
		})),
	)

	result, err := workflow.Run(context.Background(), defaultRequest("selfbootstrap-write-evidence-failed"))
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Run() error = %v, want ErrVerificationFailed", err)
	}
	if result.Report.Status != gopact.VerificationStatusFailed {
		t.Fatalf("Report.Status = %q, want failed", result.Report.Status)
	}
	requireFailedCheck(t, result.Report, "file-snapshot:go.mod")
	requireEvidenceTypes(t, result.Report, []string{gopacttest.VerificationEvidenceTypeFileSnapshot})
}

func mustWorkflow(t *testing.T, opts ...Option) *Workflow {
	t.Helper()
	base := []Option{
		WithAnalyzer(AnalyzerFunc(func(context.Context, Request) (Analysis, error) {
			return Analysis{Summary: "analysis"}, nil
		})),
		WithPlanner(PlannerFunc(func(context.Context, PlanRequest) (Plan, error) {
			return Plan{Summary: "plan", Steps: []PlanStep{{ID: "one", Summary: "do one thing"}}}, nil
		})),
		WithWriter(WriterFunc(func(context.Context, WriteRequest) (WriteResult, error) {
			return WriteResult{
				Summary: "patch",
				Diff: &gopacttest.DiffSnapshot{
					ID:         "diff:worktree",
					Ref:        "git:worktree",
					Diff:       "diff --git a/file.go b/file.go\n",
					Files:      []string{"file.go"},
					Insertions: 1,
				},
				FileSnapshots: []gopacttest.FileSnapshot{
					{ID: "file-snapshot:go.mod", Path: "go.mod", Hash: "abc123", HashAlgorithm: "sha256", SizeBytes: 10},
				},
			}, nil
		})),
		WithTester(TesterFunc(func(context.Context, TestRequest) (TestResult, error) {
			return TestResult{
				RequiredGates: []string{gopacttest.SelfBootstrapCIGateUnit},
				Commands: []gopacttest.CommandResult{
					{ID: "command:go test -count=1 ./...", Command: []string{"go", "test", "-count=1", "./..."}, ExitCode: 0},
				},
				Gates: []gopacttest.CIGateResult{
					{Gate: gopacttest.SelfBootstrapCIGateUnit, Result: gopacttest.CommandResult{Command: []string{"go", "test", "-count=1", "./..."}, ExitCode: 0}},
				},
			}, nil
		})),
		WithReviewer(ReviewerFunc(func(context.Context, ReviewRequest) (gopacttest.ReviewResult, error) {
			return gopacttest.ReviewResult{ID: "review:selfbootstrap", Reviewer: "ci-reviewer", Source: "mock", Status: gopacttest.ReviewStatusApproved}, nil
		})),
	}
	base = append(base, opts...)
	workflow, err := New(base...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return workflow
}

func defaultRequest(runID string) Request {
	return Request{
		Objective:  "ship a self-bootstrap slice",
		Repository: "gopact-ext",
		IDs:        gopact.RuntimeIDs{RunID: runID, ThreadID: "thread-1", AgentID: "devagent-selfbootstrap"},
	}
}

func requireWorkflowNodes(t *testing.T, export gopact.RunExport, want []string) {
	t.Helper()
	var got []string
	for _, step := range export.Steps {
		got = append(got, step.Node)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow nodes = %+v, want %+v", got, want)
	}
}

func requireEvidenceTypes(t *testing.T, report gopact.VerificationReport, want []string) {
	t.Helper()
	got := map[string]bool{}
	for _, check := range report.Checks {
		for _, evidence := range check.Evidence {
			got[evidence.Type] = true
		}
	}
	for _, evidenceType := range want {
		if !got[evidenceType] {
			t.Fatalf("report missing evidence type %q; checks=%+v", evidenceType, report.Checks)
		}
	}
}

func requireFailedCheck(t *testing.T, report gopact.VerificationReport, id string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			if check.Status != gopact.VerificationStatusFailed {
				t.Fatalf("check %q status = %q, want failed", id, check.Status)
			}
			return
		}
	}
	t.Fatalf("check %q not found; checks=%+v", id, report.Checks)
}

func requireEventTypes(t *testing.T, export gopact.RunExport, want []gopact.EventType) {
	t.Helper()
	got := map[gopact.EventType]bool{}
	for _, event := range export.Events {
		got[event.Type] = true
	}
	for _, eventType := range want {
		if !got[eventType] {
			t.Fatalf("run export missing event type %q; events=%+v", eventType, export.Events)
		}
	}
}
