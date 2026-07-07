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
	ErrPlannerRequired   = errors.New("planexec: planner is required")
	ErrExecutorRequired  = errors.New("planexec: executor is required")
	ErrReplannerRequired = errors.New("planexec: replanner is required")
	ErrInvalidInput      = errors.New("planexec: invalid input")
	// ErrModelRequired reports a missing model for NewModelAgent.
	ErrModelRequired = errors.New("planexec: model is required")
	// ErrModelPlanEmpty reports a model plan response without executable steps.
	ErrModelPlanEmpty = errors.New("planexec: model returned no plan steps")
	// ErrApprovalPolicyRequired reports a missing approval policy.
	ErrApprovalPolicyRequired = errors.New("planexec: approval policy is required")
	// ErrCheckpointRequired reports a missing checkpoint store.
	ErrCheckpointRequired = errors.New("planexec: checkpoint store is required")
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

type ReplanRequest struct {
	Task       string
	Steps      []Step
	Results    []StepResult
	FailedStep Step
	Err        error
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

type Replanner interface {
	Replan(context.Context, ReplanRequest) ([]Step, error)
}

type ReplannerFunc func(context.Context, ReplanRequest) ([]Step, error)

func (f ReplannerFunc) Replan(ctx context.Context, request ReplanRequest) ([]Step, error) {
	if f == nil {
		return nil, ErrReplannerRequired
	}
	return f(ctx, request)
}

type Agent struct {
	runnable         *graph.Runnable[State]
	approval         gopact.Policy
	checkpointer     graph.Checkpointer[State]
	checkpointLoader graph.CheckpointLoader[State]
	modelOptions     []gopact.ModelRequestOption
	replanner        Replanner
}

var _ gopact.StateRunnable[State] = (*Agent)(nil)

// Option configures a plan-execute agent.
type Option func(*Agent) error

// WithApprovalPolicy requires approval before executing planned steps.
func WithApprovalPolicy(policy gopact.Policy) Option {
	return func(agent *Agent) error {
		if policy == nil {
			return ErrApprovalPolicyRequired
		}
		agent.approval = policy
		return nil
	}
}

// WithCheckpointStore writes checkpoints and resumes from the latest checkpoint for the run ThreadID.
func WithCheckpointStore(store graph.CheckpointStore[State]) Option {
	return func(agent *Agent) error {
		if store == nil {
			return ErrCheckpointRequired
		}
		agent.checkpointer = store
		agent.checkpointLoader = store
		return nil
	}
}

// WithModelOptions applies request options to every model-backed planner and executor call.
func WithModelOptions(opts ...gopact.ModelRequestOption) Option {
	return func(agent *Agent) error {
		for _, opt := range opts {
			if opt != nil {
				agent.modelOptions = append(agent.modelOptions, opt)
			}
		}
		return nil
	}
}

// WithReplanner retries execution once with a replacement plan after an execution failure.
func WithReplanner(replanner Replanner) Option {
	return func(agent *Agent) error {
		if replanner == nil {
			return ErrReplannerRequired
		}
		agent.replanner = replanner
		return nil
	}
}

func New(planner Planner, executor Executor, opts ...Option) (*Agent, error) {
	if planner == nil {
		return nil, ErrPlannerRequired
	}
	if executor == nil {
		return nil, ErrExecutorRequired
	}
	agent := &Agent{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(agent); err != nil {
			return nil, err
		}
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
	if agent.approval != nil {
		g.AddNode("approval", func(ctx context.Context, state State) (State, error) {
			ids, _ := gopact.RuntimeIDsFromContext(ctx)
			req := gopact.PolicyRequest{
				IDs:      ids,
				Boundary: gopact.PolicyBoundaryNode,
				Action:   gopact.PolicyActionExec,
				Input:    state,
				Metadata: map[string]any{
					"template": "planexec",
					"node":     "execute",
				},
			}
			decision, err := agent.approval.Decide(ctx, req)
			if err != nil {
				return state, fmt.Errorf("planexec: approval policy: %w", err)
			}
			switch decision.Action {
			case gopact.PolicyAllow:
				state.Trace = append(state.Trace, "approval")
				return state, nil
			case gopact.PolicyReview:
				return state, gopact.Interrupt(gopact.InterruptRecord{
					ID:         "planexec:approval",
					Type:       gopact.InterruptApproval,
					Reason:     decision.Reason,
					RequiredBy: "planexec.execute",
					ResumeSchema: gopact.JSONSchema{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []any{"approved"},
						"properties": map[string]any{
							"approved": map[string]any{"type": "boolean", "const": true},
						},
					},
					Metadata: map[string]any{
						"template":              "planexec",
						"policy_boundary":       req.Boundary,
						"policy_request_action": req.Action,
					},
				})
			default:
				return state, &gopact.PolicyDeniedError{Decision: decision, Request: req}
			}
		})
	}
	g.AddNode("execute", func(ctx context.Context, state State) (State, error) {
		replanned := false
		for {
			state.Results = state.Results[:0]
			var failedStep Step
			var failedErr error
			for _, step := range state.Steps {
				result, err := executor.Execute(ctx, step)
				if err != nil {
					failedStep = step
					failedErr = err
					break
				}
				state.Results = append(state.Results, result)
			}
			if failedErr == nil {
				state.Trace = append(state.Trace, "execute")
				return state, nil
			}
			if agent.replanner == nil || replanned {
				return state, failedErr
			}
			steps, err := agent.replanner.Replan(ctx, ReplanRequest{
				Task:       state.Task,
				Steps:      append([]Step(nil), state.Steps...),
				Results:    append([]StepResult(nil), state.Results...),
				FailedStep: failedStep,
				Err:        failedErr,
			})
			if err != nil {
				return state, err
			}
			state.Steps = append([]Step(nil), steps...)
			state.Trace = append(state.Trace, "replan")
			replanned = true
		}
	})
	g.AddNode("summarize", func(_ context.Context, state State) (State, error) {
		state.Summary = fmt.Sprintf("completed %d steps", len(state.Results))
		state.Trace = append(state.Trace, "summarize")
		return state, nil
	})
	g.AddEdge(graph.Start, "plan")
	if agent.approval != nil {
		g.AddEdge("plan", "approval")
		g.AddEdge("approval", "execute")
	} else {
		g.AddEdge("plan", "execute")
	}
	g.AddEdge("execute", "summarize")
	g.AddEdge("summarize", graph.End)

	runnable, err := g.Compile()
	if err != nil {
		return nil, err
	}
	agent.runnable = runnable
	return agent, nil
}

// NewModelAgent creates a plan-execute agent backed by one response model.
func NewModelAgent(model gopact.ResponseModel, opts ...Option) (*Agent, error) {
	if model == nil {
		return nil, ErrModelRequired
	}
	agent := &Agent{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(agent); err != nil {
			return nil, err
		}
	}
	client := modelBackedAgent{model: model, opts: append([]gopact.ModelRequestOption(nil), agent.modelOptions...)}
	return New(client, client, opts...)
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
	request := gopact.NewModelRequest(opts...)
	if ids, ok := gopact.RuntimeIDsFromContext(ctx); ok && !ids.IsZero() {
		request.IDs = request.IDs.WithDefaults(ids)
	}
	response, err := a.model.Generate(ctx, request)
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
		if ctx == nil {
			ctx = context.TODO()
		}
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
			ctx = gopact.ContextWithRuntimeIDs(ctx, runCfg.IDs)
			invokeOpts = append(invokeOpts, graph.WithRuntimeIDs(runCfg.IDs))
		}
		if runCfg.StepExport != nil {
			invokeOpts = append(invokeOpts, graph.WithStepExport(*runCfg.StepExport))
		}
		if runCfg.ResumeRequest != nil {
			invokeOpts = append(invokeOpts, graph.WithResumeRequest(*runCfg.ResumeRequest))
		}
		if runCfg.JSONSchemaValidator != nil {
			invokeOpts = append(invokeOpts, graph.WithJSONSchemaValidator(runCfg.JSONSchemaValidator))
		}
		if a.checkpointer != nil {
			invokeOpts = append(invokeOpts, graph.WithCheckpointer(a.checkpointer))
		}
		if a.checkpointLoader != nil {
			invokeOpts = append(invokeOpts, graph.WithCheckpointLoader(a.checkpointLoader))
		}
		for event, err := range a.runnable.Run(ctx, state, invokeOpts...) {
			if !yield(event, err) {
				return
			}
		}
	}
}

// Invoke runs the agent through the typed result-first API.
func (a *Agent) Invoke(ctx context.Context, input State, opts ...gopact.RunOption) (State, error) {
	var output State
	cfg := gopact.ResolveRunOptions(opts...)
	for event, err := range a.Run(ctx, input, opts...) {
		if cfg.EventSink != nil {
			if sinkErr := cfg.EventSink.Emit(ctx, event); sinkErr != nil {
				return output, sinkErr
			}
		}
		if snapshot := event.StepSnapshot; snapshot != nil {
			if state, ok := snapshot.Output.(State); ok {
				output = state
			}
		}
		if err != nil {
			return output, err
		}
	}
	return output, nil
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
