// Package planexec provides a minimal plan-execute agent template.
package planexec

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/graph"
)

var (
	ErrPlannerRequired  = errors.New("planexec: planner is required")
	ErrExecutorRequired = errors.New("planexec: executor is required")
	ErrInvalidInput     = errors.New("planexec: invalid input")
)

type State struct {
	Task    string
	Steps   []Step
	Results []StepResult
	Trace   []string
	Summary string
}

type Step struct {
	ID          string
	Instruction string
}

type StepResult struct {
	StepID string
	Output string
}

type PlanRequest struct {
	Task string
}

type Planner interface {
	Plan(context.Context, PlanRequest) ([]Step, error)
}

type PlannerFunc func(context.Context, PlanRequest) ([]Step, error)

func (f PlannerFunc) Plan(ctx context.Context, request PlanRequest) ([]Step, error) {
	if f == nil {
		return nil, ErrPlannerRequired
	}
	return f(ctx, request)
}

type Executor interface {
	Execute(context.Context, Step) (StepResult, error)
}

type ExecutorFunc func(context.Context, Step) (StepResult, error)

func (f ExecutorFunc) Execute(ctx context.Context, step Step) (StepResult, error) {
	if f == nil {
		return StepResult{}, ErrExecutorRequired
	}
	return f(ctx, step)
}

type Agent struct {
	runnable *graph.Runnable[State]
}

func New(planner Planner, executor Executor) (*Agent, error) {
	if planner == nil {
		return nil, ErrPlannerRequired
	}
	if executor == nil {
		return nil, ErrExecutorRequired
	}

	g := graph.New[State]()
	g.AddNode("plan", func(ctx context.Context, state State) (State, error) {
		steps, err := planner.Plan(ctx, PlanRequest{Task: state.Task})
		if err != nil {
			return state, err
		}
		state.Steps = append([]Step(nil), steps...)
		state.Trace = append(state.Trace, "plan")
		return state, nil
	})
	g.AddNode("execute", func(ctx context.Context, state State) (State, error) {
		state.Results = state.Results[:0]
		for _, step := range state.Steps {
			result, err := executor.Execute(ctx, step)
			if err != nil {
				return state, err
			}
			state.Results = append(state.Results, result)
		}
		state.Trace = append(state.Trace, "execute")
		return state, nil
	})
	g.AddNode("summarize", func(_ context.Context, state State) (State, error) {
		state.Summary = fmt.Sprintf("completed %d steps", len(state.Results))
		state.Trace = append(state.Trace, "summarize")
		return state, nil
	})
	g.AddEdge(graph.Start, "plan")
	g.AddEdge("plan", "execute")
	g.AddEdge("execute", "summarize")
	g.AddEdge("summarize", graph.End)

	runnable, err := g.Compile()
	if err != nil {
		return nil, err
	}
	return &Agent{runnable: runnable}, nil
}

func (a *Agent) Run(ctx context.Context, input any, opts ...gopact.RunOption) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		state, err := inputState(input)
		if err != nil {
			yield(gopact.Event{Type: gopact.EventRunFailed, Err: err}, err)
			return
		}
		if a == nil || a.runnable == nil {
			err := errors.New("planexec: agent is nil")
			yield(gopact.Event{Type: gopact.EventRunFailed, Err: err}, err)
			return
		}

		runCfg := gopact.ResolveRunOptions(opts...)
		invokeOpts := []graph.InvokeOption{}
		if !runCfg.IDs.IsZero() {
			invokeOpts = append(invokeOpts, graph.WithRuntimeIDs(runCfg.IDs))
		}
		for event, err := range a.runnable.Run(ctx, state, invokeOpts...) {
			if !yield(event, err) {
				return
			}
		}
	}
}

func inputState(input any) (State, error) {
	switch value := input.(type) {
	case State:
		return value, nil
	case string:
		return State{Task: value}, nil
	default:
		return State{}, fmt.Errorf("%w: got %T", ErrInvalidInput, input)
	}
}
