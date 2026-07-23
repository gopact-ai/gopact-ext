package parallel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/workflow"
)

func TestAgentReducesConcurrentBranchResultsInDeclaredOrder(t *testing.T) {
	started := make(chan string, 3)
	releases := map[string]chan struct{}{
		"first": make(chan struct{}), "second": make(chan struct{}), "third": make(chan struct{}),
	}
	children := make([]agent.Agent, 0, 3)
	for _, name := range []string{"first", "second", "third"} {
		name := name
		children = append(children, branchAgent(name, func(_ context.Context, request agent.Request) (agent.Response, error) {
			if firstMessage(request).Parts[0].Text != "shared" || request.Metadata["input"] != "original" {
				return agent.Response{}, fmt.Errorf("%s received mutated request", name)
			}
			request.Messages[0].Parts[0].Text = name
			request.Metadata["input"] = name
			started <- name
			<-releases[name]
			return agent.Response{Message: gopact.UserMessage(name + "-result")}, nil
		}))
	}
	var reduced []string
	store := workflow.NewMemoryStore()
	target, err := New(testIdentity(), compileDirectory(t, children...), []string{"first", "second", "third"}, ReducerFunc(
		func(_ context.Context, results []BranchResult) (agent.Response, error) {
			for _, result := range results {
				reduced = append(reduced, result.Name+":"+result.Response.Message.Parts[0].Text)
			}
			results[0].Response.Message.Parts[0].Text = "caller-mutation"
			return agent.Response{Message: gopact.UserMessage("done")}, nil
		},
	), WithWorkflowOptions(workflow.WithStore(store)))
	if err != nil {
		t.Fatal(err)
	}
	type invocation struct {
		response agent.Response
		err      error
	}
	done := make(chan invocation, 1)
	go func() {
		response, err := target.Invoke(context.Background(), agent.Request{
			Messages: []gopact.Message{gopact.UserMessage("shared")}, Metadata: map[string]string{"input": "original"},
		}, gopact.WithRunID("parallel-persistence"))
		done <- invocation{response: response, err: err}
	}()
	for range 3 {
		<-started
	}
	close(releases["third"])
	close(releases["second"])
	close(releases["first"])
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.response.Message.Parts[0].Text != "done" {
		t.Fatalf("response = %+v, want done", result.response)
	}
	want := []string{"first:first-result", "second:second-result", "third:third-result"}
	if !reflect.DeepEqual(reduced, want) {
		t.Fatalf("reduced = %v, want declaration order %v", reduced, want)
	}
	checkpoint, err := store.Load(context.Background(), "parallel-persistence")
	if err != nil || checkpoint.Status != workflow.CheckpointCompleted {
		t.Fatalf("Load() = %+v, %v, want completed checkpoint", checkpoint, err)
	}
}

func TestAgentHonorsWorkflowParallelism(t *testing.T) {
	started := make(chan struct{}, 5)
	release := make(chan struct{}, 5)
	var active atomic.Int32
	var maximum atomic.Int32
	children := make([]agent.Agent, 0, 5)
	var names []string
	for index := range 5 {
		name := string(rune('a' + index))
		names = append(names, name)
		children = append(children, branchAgent(name, func(context.Context, agent.Request) (agent.Response, error) {
			current := active.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			return agent.Response{Message: gopact.UserMessage(name)}, nil
		}))
	}
	target, err := New(testIdentity(), compileDirectory(t, children...), names, ReducerFunc(
		func(_ context.Context, results []BranchResult) (agent.Response, error) {
			return results[0].Response, nil
		},
	), WithMaxParallelism(2))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := target.Invoke(context.Background(), agent.Request{})
		done <- err
	}()
	<-started
	<-started
	select {
	case <-started:
		t.Fatal("third branch started before a Workflow parallel slot was released")
	default:
	}
	for range 5 {
		release <- struct{}{}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 2 {
		t.Fatalf("maximum parallelism = %d, want 2", maximum.Load())
	}
}

func TestAgentUsesWorkflowFanoutAndMergeFacts(t *testing.T) {
	first := branchAgent("first", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("first")}, nil
	})
	second := branchAgent("second", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("second")}, nil
	})
	target, err := New(testIdentity(), compileDirectory(t, first, second), []string{"first", "second"}, ReducerFunc(
		func(_ context.Context, _ []BranchResult) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("done")}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	var nodes []string
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("parallel-workflow"), gopact.WithStrictEventHandler(
		func(_ context.Context, event gopact.Event) error {
			if event.RunID == "parallel-workflow" && event.Type == workflow.EventNodeCompleted {
				nodes = append(nodes, event.NodeID)
			}
			return nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plan", "child.first", "child.second", "merge"}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("completed nodes = %v, want Workflow fanout facts %v", nodes, want)
	}
}

func TestAgentPreservesRootFailureAndCancelsUnstartedBranches(t *testing.T) {
	firstStarted := make(chan struct{})
	first := branchAgent("first", func(ctx context.Context, _ agent.Request) (agent.Response, error) {
		close(firstStarted)
		<-ctx.Done()
		return agent.Response{}, ctx.Err()
	})
	boom := errors.New("second failed")
	second := branchAgent("second", func(context.Context, agent.Request) (agent.Response, error) {
		<-firstStarted
		return agent.Response{}, boom
	})
	var thirdCalls atomic.Int32
	third := branchAgent("third", func(context.Context, agent.Request) (agent.Response, error) {
		thirdCalls.Add(1)
		return agent.Response{}, nil
	})
	target, err := New(testIdentity(), compileDirectory(t, first, second, third), []string{"first", "second", "third"}, ReducerFunc(
		func(_ context.Context, results []BranchResult) (agent.Response, error) {
			return results[0].Response, nil
		},
	), WithMaxParallelism(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{}); !errors.Is(err, boom) {
		t.Fatalf("Invoke() error = %v, want root branch failure", err)
	}
	if thirdCalls.Load() != 0 {
		t.Fatalf("third calls = %d, want unstarted branch canceled", thirdCalls.Load())
	}
}

func TestAgentResumesAllInterruptedBranchesWithoutReplayingCompletedBranch(t *testing.T) {
	var completedRuns, firstRuns, secondRuns int
	completed := branchAgent("completed", func(context.Context, agent.Request) (agent.Response, error) {
		completedRuns++
		return agent.Response{Message: gopact.UserMessage("completed")}, nil
	})
	first := interruptingWorkflowAgent(t, "first", "approval-first", &firstRuns)
	second := interruptingWorkflowAgent(t, "second", "approval-second", &secondRuns)
	target, err := New(testIdentity(), compileDirectory(t, completed, first, second), []string{"completed", "first", "second"}, ReducerFunc(
		func(_ context.Context, results []BranchResult) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage(
				results[0].Response.Message.Parts[0].Text + "/" +
					results[1].Response.Message.Parts[0].Text + "/" +
					results[2].Response.Message.Parts[0].Text,
			)}, nil
		},
	), WithMaxParallelism(3))
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("parallel-parent"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want Workflow InterruptError", err)
	}
	if got, want := requestIDs(interrupted.Requests), []string{"approval-first", "approval-second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("interrupts = %v, want %v", got, want)
	}
	if completedRuns != 1 || firstRuns != 0 || secondRuns != 0 {
		t.Fatalf("runs before resume = %d/%d/%d, want 1/0/0", completedRuns, firstRuns, secondRuns)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{
		RunID: interrupted.RunID, CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{
			{InterruptID: "approval-first", PayloadRef: "resolution://first"},
			{InterruptID: "approval-second", PayloadRef: "resolution://second"},
		},
	}))
	if err != nil {
		t.Fatalf("resumed Invoke() error = %v", err)
	}
	if response.Message.Parts[0].Text != "completed/first/second" {
		t.Fatalf("response = %+v, want merged results", response)
	}
	if completedRuns != 1 || firstRuns != 1 || secondRuns != 1 {
		t.Fatalf("runs after resume = %d/%d/%d, want 1/1/1", completedRuns, firstRuns, secondRuns)
	}
}

func TestNewRejectsInvalidParallelConfiguration(t *testing.T) {
	directory := compileDirectory(t, branchAgent("one", nil))
	reducer := ReducerFunc(func(_ context.Context, results []BranchResult) (agent.Response, error) {
		return results[0].Response, nil
	})
	if _, err := New(testIdentity(), directory, nil, reducer); err == nil {
		t.Fatal("New(empty children) error = nil")
	}
	if _, err := New(testIdentity(), directory, []string{"missing"}, reducer); err == nil {
		t.Fatal("New(unknown child) error = nil")
	}
	if _, err := New(testIdentity(), directory, []string{"one", "one"}, reducer); err == nil {
		t.Fatal("New(duplicate child) error = nil")
	}
	if _, err := New(testIdentity(), directory, []string{"one"}, nil); err == nil {
		t.Fatal("New(nil reducer) error = nil")
	}
	if _, err := New(testIdentity(), directory, []string{"one"}, reducer, WithMaxParallelism(0)); err == nil {
		t.Fatal("New(zero parallelism) error = nil")
	}
}

func TestAgentConformance(t *testing.T) {
	children := []agent.Agent{branchAgent("one", nil), branchAgent("two", nil)}
	target, err := New(testIdentity(), compileDirectory(t, children...), []string{"one", "two"}, ReducerFunc(
		func(_ context.Context, _ []BranchResult) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("done")}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	gopacttest.RequireWorkflowAgentConformance(t, gopacttest.AgentConformanceCase{
		Agent: target, Request: agent.Request{Messages: []gopact.Message{gopact.UserMessage("work")}},
		Validate: func(response agent.Response) error {
			if len(response.Message.Parts) != 1 || response.Message.Parts[0].Text != "done" {
				return errors.New("unexpected response")
			}
			return nil
		},
	})
}

type testBranch struct {
	identity agent.Identity
	invoke   func(context.Context, agent.Request) (agent.Response, error)
}

func branchAgent(name string, invoke func(context.Context, agent.Request) (agent.Response, error)) *testBranch {
	if invoke == nil {
		invoke = func(_ context.Context, request agent.Request) (agent.Response, error) {
			return agent.Response{Message: firstMessage(request)}, nil
		}
	}
	return &testBranch{identity: agent.Identity{Name: name, Description: name + " branch", Version: "v1"}, invoke: invoke}
}

func (branch *testBranch) Identity() agent.Identity { return branch.identity }

func (branch *testBranch) Invoke(ctx context.Context, request agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	return branch.invoke(ctx, request)
}

func interruptingWorkflowAgent(t *testing.T, name, interruptID string, runs *int) agent.Agent {
	t.Helper()
	identity := agent.Identity{Name: name, Description: name + " branch", Version: "v1"}
	wf := workflow.New[agent.Request, agent.Response](name, workflow.WithTopologyVersion(identity.Version))
	work := wf.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		*runs++
		return agent.Response{Message: gopact.UserMessage(name)}, nil
	})
	work.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{ID: interruptID}}, nil
		},
	)))
	wf.Entry(work)
	wf.Exit(work)
	target, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func requestIDs(requests []workflow.InterruptRequest) []string {
	ids := make([]string, len(requests))
	for index, request := range requests {
		ids[index] = request.ID
	}
	return ids
}

func compileDirectory(t *testing.T, children ...agent.Agent) *agent.Directory {
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

func firstMessage(request agent.Request) gopact.Message {
	if len(request.Messages) == 0 {
		return gopact.UserMessage("")
	}
	return request.Messages[0]
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "parallel", Description: "runs child branches", Version: "v1"}
}
