package deep

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

func TestDeepExecutesTaskPlanAndCarriesArtifactContext(t *testing.T) {
	var plannerCalls int
	var childRequests []agent.Request
	store := workflow.NewMemoryStore()
	directory := testDirectory(t,
		testChild("research", func(_ context.Context, request agent.Request) (agent.Response, error) {
			childRequests = append(childRequests, cloneRequest(request))
			return agent.Response{
				Message: gopact.UserMessage("evidence"), Artifacts: []gopact.ArtifactRef{{URI: "artifact://evidence"}},
			}, nil
		}),
		testChild("write", func(_ context.Context, request agent.Request) (agent.Response, error) {
			childRequests = append(childRequests, cloneRequest(request))
			return agent.Response{Message: gopact.UserMessage("report")}, nil
		}),
	)
	target := newTestAgent(t, directory, PlannerFunc(func(context.Context, PlanInput) ([]Task, error) {
		plannerCalls++
		return []Task{
			{ID: "research", Description: "collect evidence", AgentName: "research"},
			{ID: "write", Description: "write report", AgentName: "write"},
		}, nil
	}), WithWorkflowOptions(workflow.WithStore(store)))
	var nodes []string
	request := agent.Request{
		Messages:  []gopact.Message{gopact.UserMessage("investigate")},
		Artifacts: []gopact.ArtifactRef{{URI: "artifact://input"}},
		Metadata:  map[string]string{"tenant": "example"},
	}
	response, err := target.Invoke(
		context.Background(),
		request,
		gopact.WithRunID("deep-workflow"),
		gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
			if event.RunID == "deep-workflow" && event.Type == workflow.EventNodeCompleted {
				nodes = append(nodes, event.NodeID)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "report" || plannerCalls != 1 {
		t.Fatalf("response/planner = %+v/%d", response, plannerCalls)
	}
	wantChildRequests := []agent.Request{
		{Messages: request.Messages, Metadata: request.Metadata},
		{Messages: request.Messages, Artifacts: []gopact.ArtifactRef{{URI: "artifact://evidence"}}, Metadata: request.Metadata},
	}
	if !reflect.DeepEqual(childRequests, wantChildRequests) {
		t.Fatalf("child requests = %+v, want typed state projection %+v", childRequests, wantChildRequests)
	}
	want := []string{
		"plan", "accept-plan", "continue", "build-context", "execute-task", "record-task",
		"continue", "build-context", "execute-task", "record-task", "continue", "finish",
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("completed nodes = %v, want %v", nodes, want)
	}
	checkpoint, err := store.Load(context.Background(), "deep-workflow")
	if err != nil || checkpoint.Status != workflow.CheckpointCompleted {
		t.Fatalf("Load() = %+v, %v, want completed checkpoint", checkpoint, err)
	}
}

func TestDeepRejectsInvalidTasksBeforeExecution(t *testing.T) {
	tests := []struct {
		name    string
		tasks   []Task
		options []Option
		want    string
	}{
		{name: "completed status", tasks: []Task{{ID: "t1", Description: "work", AgentName: "worker", Status: TaskCompleted}}, want: "status"},
		{name: "duplicate id", tasks: []Task{{ID: "t1", Description: "one", AgentName: "worker"}, {ID: "t1", Description: "two", AgentName: "worker"}}, want: "duplicate"},
		{name: "unknown child", tasks: []Task{{ID: "t1", Description: "work", AgentName: "missing"}}, want: "missing"},
		{name: "task limit", tasks: []Task{{ID: "t1", Description: "one", AgentName: "worker"}, {ID: "t2", Description: "two", AgentName: "worker"}}, options: []Option{WithMaxTasks(1)}, want: "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			childCalls := 0
			target := newTestAgent(t, testDirectory(t, testChild("worker", func(context.Context, agent.Request) (agent.Response, error) {
				childCalls++
				return agent.Response{Message: gopact.UserMessage("done")}, nil
			})), PlannerFunc(func(context.Context, PlanInput) ([]Task, error) {
				return tt.tasks, nil
			}), tt.options...)
			_, err := target.Invoke(context.Background(), agent.Request{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || childCalls != 0 {
				t.Fatalf("Invoke() error/calls = %v/%d, want %q before execution", err, childCalls, tt.want)
			}
		})
	}
}

func TestDeepResumesInterruptedTaskWithoutReplanningOrReplay(t *testing.T) {
	var childRuns, plannerCalls int
	childIdentity := agent.Identity{Name: "worker", Description: "requires approval", Version: "v1"}
	childWorkflow := workflow.New[agent.Request, agent.Response](childIdentity.Name, workflow.WithTopologyVersion(childIdentity.Version))
	work := childWorkflow.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		childRuns++
		return agent.Response{Message: gopact.UserMessage("approved")}, nil
	})
	work.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{ID: "task-approval"}}, nil
		},
	)))
	childWorkflow.Entry(work)
	childWorkflow.Exit(work)
	child, err := agent.NewWorkflowAgent(childIdentity, childWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	target := newTestAgent(t, testDirectory(t, child), PlannerFunc(func(context.Context, PlanInput) ([]Task, error) {
		plannerCalls++
		return []Task{{ID: "t1", Description: "work", AgentName: "worker"}}, nil
	}))
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("deep-parent"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want Workflow InterruptError", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{
		RunID: "deep-parent", CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: "task-approval", PayloadRef: "resolution://approved"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "approved" || childRuns != 1 || plannerCalls != 1 {
		t.Fatalf("response/child/planner = %+v/%d/%d, want same task continuation", response, childRuns, plannerCalls)
	}
}

func TestDeepResumeDoesNotRepeatRecordedTask(t *testing.T) {
	var childCalls, plannerCalls int
	target := newTestAgent(t, testDirectory(t, testChild("worker", func(context.Context, agent.Request) (agent.Response, error) {
		childCalls++
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})), PlannerFunc(func(context.Context, PlanInput) ([]Task, error) {
		plannerCalls++
		return []Task{{ID: "t1", Description: "work", AgentName: "worker"}}, nil
	}))
	sinkErr := errors.New("record sink failed")
	failed := false
	_, err := target.Invoke(
		context.Background(), agent.Request{}, gopact.WithRunID("deep-resume"),
		gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
			if event.RunID == "deep-resume" && event.Type == workflow.EventNodeCompleted && event.NodeID == "record-task" && !failed {
				failed = true
				return sinkErr
			}
			return nil
		}),
	)
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Invoke() error = %v, want sink failure", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{RunID: "deep-resume"}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "done" || childCalls != 1 || plannerCalls != 1 {
		t.Fatalf("response/child/planner = %+v/%d/%d, want committed task reused", response, childCalls, plannerCalls)
	}
}

func TestDeepAgentConformance(t *testing.T) {
	target := newTestAgent(t, testDirectory(t, testChild("worker", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})), PlannerFunc(func(context.Context, PlanInput) ([]Task, error) {
		return []Task{{ID: "t1", Description: "work", AgentName: "worker"}}, nil
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

func newTestAgent(t *testing.T, directory *agent.Directory, planner Planner, options ...Option) *Agent {
	t.Helper()
	target, err := New(testIdentity(), directory, planner, options...)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "deep", Description: "long-horizon tasks", Version: "v1"}
}
