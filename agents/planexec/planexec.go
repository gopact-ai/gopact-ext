// Package planexec provides a minimal plan-execute agent template.
package planexec

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/graph"
)

var (
	ErrPlannerRequired  = errors.New("planexec: planner is required")
	ErrExecutorRequired = errors.New("planexec: executor is required")
	ErrInvalidInput     = errors.New("planexec: invalid input")
	// ErrModelRequired reports a missing model for NewModelAgent.
	ErrModelRequired = errors.New("planexec: model is required")
	// ErrModelPlanEmpty reports a model plan response without executable steps.
	ErrModelPlanEmpty = errors.New("planexec: model returned no plan steps")
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

// NewModelAgent creates a plan-execute agent backed by one response model.
func NewModelAgent(model gopact.ResponseModel, opts ...gopact.ModelRequestOption) (*Agent, error) {
	if model == nil {
		return nil, ErrModelRequired
	}
	client := modelBackedAgent{model: model, opts: append([]gopact.ModelRequestOption(nil), opts...)}
	return New(client, client)
}

type modelBackedAgent struct {
	model gopact.ResponseModel
	opts  []gopact.ModelRequestOption
}

func (a modelBackedAgent) Plan(ctx context.Context, request PlanRequest) ([]Step, error) {
	text, err := a.generate(ctx,
		gopact.SystemMessage("Split the task into executable steps. Return one step per line. Prefix each line with STEP:."),
		gopact.UserMessage(request.Task),
	)
	if err != nil {
		return nil, err
	}
	steps := modelPlanSteps(text)
	if len(steps) == 0 {
		return nil, ErrModelPlanEmpty
	}
	return steps, nil
}

func (a modelBackedAgent) Execute(ctx context.Context, step Step) (StepResult, error) {
	text, err := a.generate(ctx,
		gopact.SystemMessage("Execute the plan step. Return concise plain text."),
		gopact.UserMessage(step.Instruction),
	)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{StepID: step.ID, Output: firstModelLine(text)}, nil
}

func (a modelBackedAgent) generate(ctx context.Context, messages ...gopact.Message) (string, error) {
	opts := append([]gopact.ModelRequestOption{gopact.WithMessages(messages...)}, a.opts...)
	response, err := a.model.Generate(ctx, gopact.NewModelRequest(opts...))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(response.Message.Text()), nil
}

func modelPlanSteps(text string) []Step {
	var steps []Step
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "STEP:"):
			line = strings.TrimSpace(line[len("STEP:"):])
		case strings.HasPrefix(upper, "STEP "):
			line = strings.TrimSpace(strings.TrimLeft(line[len("STEP "):], "0123456789:.) \t"))
		default:
			line = strings.TrimSpace(strings.TrimLeft(line, "0123456789:.) \t"))
		}
		if line == "" {
			continue
		}
		steps = append(steps, Step{
			ID:          fmt.Sprintf("step-%d", len(steps)+1),
			Instruction: line,
		})
	}
	return steps
}

func firstModelLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return strings.TrimSpace(text)
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
