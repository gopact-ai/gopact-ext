package planexec

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/workflow"
)

func TestNewRequiresPipelineOptions(t *testing.T) {
	_, err := New(testIdentity())
	if err == nil {
		t.Fatal("New() error = nil")
	}
	for _, name := range []string{"directory", "planner", "replanner", "reporter"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("New() error = %v, want %q", err, name)
		}
	}
}

func TestPlanExecRunsReplacementPlanThroughWorkflowFacts(t *testing.T) {
	var childCalls, plannerCalls, replannerCalls int
	directory := testDirectory(t, testChild("worker", func(_ context.Context, request agent.Request) (agent.Response, error) {
		childCalls++
		return agent.Response{Message: gopact.UserMessage("done:" + request.Messages[0].Parts[0].Text)}, nil
	}))
	store := workflow.NewMemoryStore()
	target, err := New(
		testIdentity(),
		WithDirectory(directory),
		WithPlanner(PlannerFunc(func(_ context.Context, input PlanInput) (Plan, error) {
			plannerCalls++
			return testPlan("p1", 1, "s1"), nil
		})),
		WithReplanner(ReplannerFunc(func(_ context.Context, input ReplanInput) (ReplanDecision, error) {
			replannerCalls++
			if len(input.Results) == 1 {
				plan := testPlan("p2", 2, "s2")
				return ReplanDecision{Plan: &plan}, nil
			}
			return ReplanDecision{Done: true}, nil
		})),
		WithReporter(ReporterFunc(func(_ context.Context, input ReportInput) (agent.Response, error) {
			return input.Results[len(input.Results)-1].Response, nil
		})),
		WithWorkflowOptions(workflow.WithCheckpointer(store), workflow.WithJournal(store)),
	)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []string
	response, err := target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("input")}},
		gopact.WithRunID("planexec-workflow"),
		gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
			if event.RunID == "planexec-workflow" && event.Type == workflow.EventNodeCompleted {
				nodes = append(nodes, event.NodeID)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "done:input" || childCalls != 2 || plannerCalls != 1 || replannerCalls != 2 {
		t.Fatalf("response/calls = %+v/%d/%d/%d", response, childCalls, plannerCalls, replannerCalls)
	}
	want := []string{
		"plan", "accept-plan", "continue", "dispatch-step", "execute-step", "record-step", "replan",
		"accept-replan", "continue", "dispatch-step", "execute-step", "record-step", "replan", "continue", "report",
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("completed nodes = %v, want %v", nodes, want)
	}
	checkpoint, err := store.Load(context.Background(), "planexec-workflow")
	if err != nil || checkpoint.Status != workflow.CheckpointCompleted {
		t.Fatalf("Load() = %+v, %v, want completed checkpoint", checkpoint, err)
	}
}

func TestPlanExecRejectsInvalidPlansBeforeStepExecution(t *testing.T) {
	tests := []struct {
		name string
		plan Plan
		want string
	}{
		{name: "planner status", plan: Plan{ID: "p", Version: 1, Steps: []Step{{ID: "s", Description: "work", AgentName: "worker", Status: StepCompleted}}}, want: "status"},
		{name: "unknown child", plan: Plan{ID: "p", Version: 1, Steps: []Step{{ID: "s", Description: "work", AgentName: "missing"}}}, want: "missing"},
		{name: "duplicate step", plan: Plan{ID: "p", Version: 1, Steps: []Step{{ID: "s", Description: "one", AgentName: "worker"}, {ID: "s", Description: "two", AgentName: "worker"}}}, want: "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			childCalls := 0
			target := newTestAgent(t, testDirectory(t, countedChild("worker", &childCalls)), PlannerFunc(
				func(context.Context, PlanInput) (Plan, error) { return tt.plan, nil },
			), ReplannerFunc(func(context.Context, ReplanInput) (ReplanDecision, error) {
				return ReplanDecision{Done: true}, nil
			}))
			_, err := target.Invoke(context.Background(), agent.Request{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || childCalls != 0 {
				t.Fatalf("Invoke() error/calls = %v/%d, want %q before execution", err, childCalls, tt.want)
			}
		})
	}
}

func TestPlanExecRejectsNonMonotonicReplacement(t *testing.T) {
	target := newTestAgent(t, testDirectory(t, countedChild("worker", new(int))), PlannerFunc(
		func(context.Context, PlanInput) (Plan, error) { return testPlan("p1", 1, "s1"), nil },
	), ReplannerFunc(func(context.Context, ReplanInput) (ReplanDecision, error) {
		plan := testPlan("p2", 1, "s2")
		return ReplanDecision{Plan: &plan}, nil
	}))
	_, err := target.Invoke(context.Background(), agent.Request{})
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("Invoke() error = %v, want non-monotonic version rejection", err)
	}
}

func TestPlanExecResumesInterruptedStepWithoutReplanningOrReplay(t *testing.T) {
	var childRuns, plannerCalls, replannerCalls int
	childIdentity := agent.Identity{Name: "worker", Description: "requires approval", Version: "v1"}
	childWorkflow := workflow.New[agent.Request, agent.Response](childIdentity.Name, workflow.WithTopologyVersion(childIdentity.Version))
	work := childWorkflow.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		childRuns++
		return agent.Response{Message: gopact.UserMessage("approved")}, nil
	})
	work.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{ID: "step-approval"}}, nil
		},
	)))
	childWorkflow.Entry(work)
	childWorkflow.Exit(work)
	child, err := agent.NewWorkflowAgent(childIdentity, childWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	target := newTestAgent(t, testDirectory(t, child), PlannerFunc(func(context.Context, PlanInput) (Plan, error) {
		plannerCalls++
		return testPlan("p1", 1, "s1"), nil
	}), ReplannerFunc(func(context.Context, ReplanInput) (ReplanDecision, error) {
		replannerCalls++
		return ReplanDecision{Done: true}, nil
	}))
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("parent"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want Workflow InterruptError", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{
		RunID: "parent", CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: "step-approval", PayloadRef: "resolution://approved"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "approved" || childRuns != 1 || plannerCalls != 1 || replannerCalls != 1 {
		t.Fatalf("response/calls = %+v/%d/%d/%d, want same child continuation", response, childRuns, plannerCalls, replannerCalls)
	}
}

func TestPlanExecResumeDoesNotRepeatCommittedNodes(t *testing.T) {
	for _, nodeID := range []string{"accept-plan", "record-step"} {
		t.Run(nodeID, func(t *testing.T) {
			var childCalls, plannerCalls int
			target := newTestAgent(t, testDirectory(t, countedChild("worker", &childCalls)), PlannerFunc(
				func(context.Context, PlanInput) (Plan, error) {
					plannerCalls++
					return testPlan("p1", 1, "s1"), nil
				},
			), ReplannerFunc(func(context.Context, ReplanInput) (ReplanDecision, error) {
				return ReplanDecision{Done: true}, nil
			}))
			sinkErr := errors.New("node sink failed")
			failed := false
			runID := "planexec-" + nodeID
			_, err := target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(
				func(_ context.Context, event gopact.Event) error {
					if event.RunID == runID && event.Type == workflow.EventNodeCompleted && event.NodeID == nodeID && !failed {
						failed = true
						return sinkErr
					}
					return nil
				},
			))
			if !errors.Is(err, sinkErr) {
				t.Fatalf("Invoke() error = %v, want sink failure", err)
			}
			if _, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{RunID: runID})); err != nil {
				t.Fatal(err)
			}
			if plannerCalls != 1 || childCalls != 1 {
				t.Fatalf("planner/child calls = %d/%d, want no replay", plannerCalls, childCalls)
			}
		})
	}
}

func TestPlanExecEnforcesTransitionLimit(t *testing.T) {
	target, err := New(
		testIdentity(), WithDirectory(testDirectory(t, countedChild("worker", new(int)))),
		WithPlanner(PlannerFunc(func(context.Context, PlanInput) (Plan, error) { return testPlan("p1", 1, "s1"), nil })),
		WithReplanner(ReplannerFunc(func(_ context.Context, input ReplanInput) (ReplanDecision, error) {
			plan := testPlan("next", input.Plan.Version+1, "next-step")
			return ReplanDecision{Plan: &plan}, nil
		})),
		WithReporter(testReporter()), WithMaxTransitions(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{}); !errors.Is(err, ErrTransitionLimit) {
		t.Fatalf("Invoke() error = %v, want transition limit", err)
	}
}

func TestPlanExecAgentConformance(t *testing.T) {
	child := testChild("worker", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})
	target := newTestAgent(t, testDirectory(t, child), PlannerFunc(
		func(context.Context, PlanInput) (Plan, error) { return testPlan("p1", 1, "s1"), nil },
	), ReplannerFunc(func(context.Context, ReplanInput) (ReplanDecision, error) {
		return ReplanDecision{Done: true}, nil
	}))
	gopacttest.RequireAgentConformance(t, gopacttest.AgentConformanceCase{
		Agent: target, Request: agent.Request{},
		Validate: func(response agent.Response) error {
			if len(response.Message.Parts) == 0 {
				return errors.New("empty response")
			}
			return nil
		},
	})
}

type testAgentChild struct {
	identity agent.Identity
	invoke   func(context.Context, agent.Request) (agent.Response, error)
}

func testChild(name string, invoke func(context.Context, agent.Request) (agent.Response, error)) agent.Agent {
	return &testAgentChild{identity: agent.Identity{Name: name, Description: name + " child", Version: "v1"}, invoke: invoke}
}

func countedChild(name string, calls *int) agent.Agent {
	return testChild(name, func(_ context.Context, request agent.Request) (agent.Response, error) {
		*calls++
		text := "done"
		if len(request.Messages) > 0 {
			text += ":" + request.Messages[0].Parts[0].Text
		}
		return agent.Response{Message: gopact.UserMessage(text)}, nil
	})
}

func (child *testAgentChild) Identity() agent.Identity { return child.identity }

func (child *testAgentChild) Invoke(ctx context.Context, request agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	return child.invoke(ctx, request)
}

func testDirectory(t *testing.T, children ...agent.Agent) *agent.Directory {
	t.Helper()
	catalog := agent.NewCatalog()
	for _, child := range children {
		if err := catalog.Add(child); err != nil {
			t.Fatal(err)
		}
	}
	directory, err := catalog.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func newTestAgent(t *testing.T, directory *agent.Directory, planner Planner, replanner Replanner) *Agent {
	t.Helper()
	target, err := New(
		testIdentity(), WithDirectory(directory), WithPlanner(planner), WithReplanner(replanner), WithReporter(testReporter()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func testReporter() Reporter {
	return ReporterFunc(func(_ context.Context, input ReportInput) (agent.Response, error) {
		if len(input.Results) == 0 {
			return agent.Response{Message: gopact.UserMessage("done")}, nil
		}
		return input.Results[len(input.Results)-1].Response, nil
	})
}

func testPlan(id string, version int, stepID string) Plan {
	return Plan{ID: id, Version: version, Steps: []Step{{ID: stepID, Description: "work", AgentName: "worker"}}}
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "planexec", Description: "plans and executes", Version: "v1"}
}
