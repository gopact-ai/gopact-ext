// Package planexec provides plan, execute, replan, and report orchestration.
package planexec

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

const defaultMaxTransitions = 32

// StepStatus is the typed status of one plan step.
type StepStatus string

// Step statuses.
const (
	StepPending   StepStatus = "pending"
	StepCompleted StepStatus = "completed"
)

// Step is one executable plan item.
type Step struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	AgentName   string     `json:"agent_name"`
	Status      StepStatus `json:"status"`
}

// Plan is a versioned ordered execution plan.
type Plan struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Steps   []Step `json:"steps"`
}

// StepResult is the result of one completed step.
type StepResult struct {
	Step     Step           `json:"step"`
	Response agent.Response `json:"response"`
}

// PlanInput is passed to a Planner.
type PlanInput struct {
	Request  agent.Request
	Previous *Plan
	Results  []StepResult
}

// ReplanInput is passed to a Replanner after a step completes.
type ReplanInput struct {
	Request agent.Request
	Plan    Plan
	Results []StepResult
}

// ReplanDecision controls the next plan transition.
type ReplanDecision struct {
	Done   bool
	Plan   *Plan
	Reason string
}

// ReportInput is passed to the final Reporter.
type ReportInput struct {
	Request agent.Request
	Plan    Plan
	Results []StepResult
}

// Planner creates the initial typed plan.
type Planner interface {
	Plan(context.Context, PlanInput) (Plan, error)
}

// PlannerFunc adapts a planner function.
type PlannerFunc func(context.Context, PlanInput) (Plan, error)

// Plan calls the wrapped planner with an isolated input copy.
func (planner PlannerFunc) Plan(ctx context.Context, input PlanInput) (Plan, error) {
	if planner == nil {
		return Plan{}, errors.New("planexec: planner is nil")
	}
	return planner(ctx, clonePlanInput(input))
}

// Replanner evaluates progress and may replace the remaining plan.
type Replanner interface {
	Replan(context.Context, ReplanInput) (ReplanDecision, error)
}

// ReplannerFunc adapts a replanner function.
type ReplannerFunc func(context.Context, ReplanInput) (ReplanDecision, error)

// Replan calls the wrapped replanner with an isolated input copy.
func (replanner ReplannerFunc) Replan(ctx context.Context, input ReplanInput) (ReplanDecision, error) {
	if replanner == nil {
		return ReplanDecision{}, errors.New("planexec: replanner is nil")
	}
	return replanner(ctx, cloneReplanInput(input))
}

// Reporter produces the final business response.
type Reporter interface {
	Report(context.Context, ReportInput) (agent.Response, error)
}

// ReporterFunc adapts a reporter function.
type ReporterFunc func(context.Context, ReportInput) (agent.Response, error)

// Report calls the wrapped reporter with an isolated input copy.
func (reporter ReporterFunc) Report(ctx context.Context, input ReportInput) (agent.Response, error) {
	if reporter == nil {
		return agent.Response{}, errors.New("planexec: reporter is nil")
	}
	return reporter(ctx, cloneReportInput(input))
}

// Option configures an Agent during construction.
type Option interface{ apply(*config) }
type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct {
	directory       *agent.Directory
	planner         Planner
	replanner       Replanner
	reporter        Reporter
	maxTransitions  int
	workflowOptions []workflow.BuildOption
	validation      *contract.Validator
}

// WithDirectory selects the immutable child directory used to execute steps.
func WithDirectory(directory *agent.Directory) Option {
	return optionFunc(func(config *config) { config.directory = directory })
}

// WithPlanner selects the component that creates the initial plan.
func WithPlanner(planner Planner) Option {
	return optionFunc(func(config *config) { config.planner = planner })
}

// WithReplanner selects the component that evaluates progress after each step.
func WithReplanner(replanner Replanner) Option {
	return optionFunc(func(config *config) { config.replanner = replanner })
}

// WithReporter selects the component that produces the final response.
func WithReporter(reporter Reporter) Option {
	return optionFunc(func(config *config) { config.reporter = reporter })
}

// WithMaxTransitions bounds replanning transitions for one invocation.
func WithMaxTransitions(limit int) Option {
	return optionFunc(func(config *config) {
		config.maxTransitions = limit
		config.validation.Positive("max transitions", limit)
	})
}

// WithWorkflowOptions configures the underlying Workflow.
func WithWorkflowOptions(options ...workflow.BuildOption) Option {
	return optionFunc(func(config *config) {
		config.workflowOptions = append([]workflow.BuildOption(nil), options...)
	})
}

// State is the Agent-domain plan execution context.
type State struct {
	Request       agent.Request
	Plan          Plan
	Next          int
	Transitions   int
	Results       []StepResult
	ReadyToReport bool
}

type control struct{ Report bool }
type stepInvocation struct {
	Step    Step
	Request agent.Request
}
type replanResult struct{ Decision ReplanDecision }

// Agent executes a typed plan through one Workflow.
type Agent struct{ workflow *agent.WorkflowAgent }

var _ agent.Agent = (*Agent)(nil)

var (
	// ErrPlanExhausted reports a plan with no remaining step and no final decision.
	ErrPlanExhausted = errors.New("planexec: plan exhausted without completion")
	// ErrTransitionLimit reports that replanning exceeded the configured bound.
	ErrTransitionLimit = errors.New("transition limit reached")
)

// New creates a plan-execute Agent from functional options.
func New(identity agent.Identity, options ...Option) (*Agent, error) {
	configuration := config{
		maxTransitions: defaultMaxTransitions, validation: contract.New("planexec").Identity("agent", identity),
	}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	if err := configuration.validation.
		Present("directory", configuration.directory).
		Present("planner", configuration.planner).
		Present("replanner", configuration.replanner).
		Present("reporter", configuration.reporter).
		Err(); err != nil {
		return nil, err
	}
	buildOptions := append([]workflow.BuildOption(nil), configuration.workflowOptions...)
	buildOptions = append(buildOptions, workflow.WithTopologyVersion(identity.Version))
	wf := workflow.New[agent.Request, agent.Response](identity.Name, buildOptions...)
	state := wf.Context(func(request agent.Request) State { return State{Request: cloneRequest(request)} })
	plannerNode := wf.Node("plan", func(ctx context.Context, request agent.Request) (Plan, error) {
		plan, err := configuration.planner.Plan(ctx, PlanInput{Request: cloneRequest(request)})
		if err != nil {
			return Plan{}, fmt.Errorf("planexec: create plan: %w", err)
		}
		return plan, nil
	})
	accept := func(ctx context.Context, plan Plan) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		accepted, err := acceptPlan(configuration.directory, plan, current.Plan.Version)
		if err != nil {
			return control{}, err
		}
		current.Plan = accepted
		current.Next = 0
		if err := state.Set(ctx, current); err != nil {
			return control{}, err
		}
		return control{}, nil
	}
	acceptInitial := wf.Node("accept-plan", accept)
	acceptReplacement := wf.Node("accept-replan", accept)
	dispatch := wf.Node("dispatch-step", func(ctx context.Context, _ control) (stepInvocation, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return stepInvocation{}, err
		}
		if current.Next >= len(current.Plan.Steps) {
			return stepInvocation{}, ErrPlanExhausted
		}
		return stepInvocation{Step: current.Plan.Steps[current.Next], Request: cloneRequest(current.Request)}, nil
	})
	stepNode := wf.AddInvokable("execute-step", stepDispatcher{directory: configuration.directory})
	record := wf.Node("record-step", func(ctx context.Context, result StepResult) (ReplanInput, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return ReplanInput{}, err
		}
		if current.Next >= len(current.Plan.Steps) || current.Plan.Steps[current.Next].ID != result.Step.ID {
			return ReplanInput{}, errors.New("planexec: completed step does not match plan cursor")
		}
		current.Plan.Steps[current.Next] = result.Step
		current.Results = append(current.Results, cloneStepResult(result))
		current.Next++
		if err := state.Set(ctx, current); err != nil {
			return ReplanInput{}, err
		}
		return replanInput(current), nil
	})
	replan := wf.Node("replan", func(ctx context.Context, input ReplanInput) (replanResult, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return replanResult{}, err
		}
		if current.Transitions >= configuration.maxTransitions {
			return replanResult{}, fmt.Errorf("planexec: %w", ErrTransitionLimit)
		}
		decision, err := configuration.replanner.Replan(ctx, input)
		if err != nil {
			return replanResult{}, fmt.Errorf("planexec: replan: %w", err)
		}
		if decision.Done && decision.Plan != nil {
			return replanResult{}, errors.New("planexec: replan cannot be done and replace plan")
		}
		current.Transitions++
		current.ReadyToReport = decision.Done
		if !decision.Done && decision.Plan == nil && current.Next >= len(current.Plan.Steps) {
			return replanResult{}, ErrPlanExhausted
		}
		if err := state.Set(ctx, current); err != nil {
			return replanResult{}, err
		}
		return replanResult{Decision: cloneReplanDecision(decision)}, nil
	})
	next := wf.Merge("continue", func(ctx context.Context, _ workflow.Inputs) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		if !current.ReadyToReport && current.Next >= len(current.Plan.Steps) {
			return control{}, ErrPlanExhausted
		}
		return control{Report: current.ReadyToReport}, nil
	})
	report := wf.Node("report", func(ctx context.Context, _ control) (agent.Response, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return agent.Response{}, err
		}
		response, err := configuration.reporter.Report(ctx, ReportInput{
			Request: cloneRequest(current.Request), Plan: clonePlan(current.Plan), Results: cloneStepResults(current.Results),
		})
		if err != nil {
			return agent.Response{}, fmt.Errorf("planexec: report: %w", err)
		}
		return cloneResponse(response), nil
	})
	replan.Route(func(_ context.Context, result replanResult) (workflow.Dispatch, error) {
		if result.Decision.Plan != nil {
			return replan.Once(acceptReplacement, clonePlan(*result.Decision.Plan)), nil
		}
		return replan.To(next), nil
	})
	next.Route(func(_ context.Context, current control) (workflow.Dispatch, error) {
		if current.Report {
			return next.Once(report, control{}), nil
		}
		return next.Once(dispatch, control{}), nil
	})
	wf.Entry(plannerNode)
	wf.Edge(plannerNode, acceptInitial)
	wf.Edge(acceptInitial, next)
	wf.Edge(acceptReplacement, next)
	wf.Edge(next, dispatch)
	wf.Edge(next, report)
	wf.Edge(dispatch, stepNode)
	wf.Edge(stepNode, record)
	wf.Edge(record, replan)
	wf.Edge(replan, acceptReplacement)
	wf.Edge(replan, next)
	wf.Exit(report)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("planexec: build workflow: %w", err)
	}
	return &Agent{workflow: facade}, nil
}

// Identity returns the immutable Agent identity.
func (target *Agent) Identity() agent.Identity {
	if target == nil || target.workflow == nil {
		return agent.Identity{}
	}
	return target.workflow.Identity()
}

// Invoke executes the current plan through completion and reports its result.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("planexec: agent is nil")
	}
	return target.workflow.Invoke(ctx, cloneRequest(request), options...)
}

type stepDispatcher struct{ directory *agent.Directory }

func (dispatcher stepDispatcher) Invoke(ctx context.Context, input stepInvocation, options ...gopact.RunOption) (StepResult, error) {
	child, ok := dispatcher.directory.Lookup(input.Step.AgentName)
	if !ok || contract.IsNil(child) {
		return StepResult{}, fmt.Errorf("planexec: step %q child %q is not in the directory", input.Step.ID, input.Step.AgentName)
	}
	response, err := child.Invoke(ctx, cloneRequest(input.Request), options...)
	if err != nil {
		return StepResult{}, fmt.Errorf("planexec: execute step %q: %w", input.Step.ID, err)
	}
	step := input.Step
	step.Status = StepCompleted
	return StepResult{Step: step, Response: cloneResponse(response)}, nil
}

func (plan Plan) validate() error {
	validator := contract.New("planexec").
		Required("plan id", plan.ID).
		Positive("plan version", plan.Version).
		NonEmpty("plan steps", len(plan.Steps))
	for index, step := range plan.Steps {
		validator.
			Required(fmt.Sprintf("step %d id", index), step.ID).
			Required(fmt.Sprintf("step %d description", index), step.Description).
			Required(fmt.Sprintf("step %d agent", index), step.AgentName).
			Check(step.Status == "" || step.Status == StepPending, "step %d status %q is owned by the runtime", index, step.Status).
			Unique("step", step.ID)
	}
	return validator.Err()
}

func acceptPlan(directory *agent.Directory, plan Plan, previous int) (Plan, error) {
	plan = clonePlan(plan)
	structureErr := plan.validate()
	validator := contract.New("planexec").
		Check(previous == 0 || plan.Version > previous, "plan version %d must be greater than %d", plan.Version, previous)
	for index, step := range plan.Steps {
		child, exists := directory.Lookup(step.AgentName)
		validator.Check(exists && !contract.IsNil(child), "step %d child %q is not in the directory", index, step.AgentName)
		plan.Steps[index].Status = StepPending
	}
	if err := errors.Join(structureErr, validator.Err()); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func replanInput(state State) ReplanInput {
	return ReplanInput{Request: cloneRequest(state.Request), Plan: clonePlan(state.Plan), Results: cloneStepResults(state.Results)}
}

func clonePlanInput(input PlanInput) PlanInput {
	input.Request = cloneRequest(input.Request)
	if input.Previous != nil {
		previous := clonePlan(*input.Previous)
		input.Previous = &previous
	}
	input.Results = cloneStepResults(input.Results)
	return input
}

func cloneReplanInput(input ReplanInput) ReplanInput {
	input.Request = cloneRequest(input.Request)
	input.Plan = clonePlan(input.Plan)
	input.Results = cloneStepResults(input.Results)
	return input
}

func cloneReportInput(input ReportInput) ReportInput {
	input.Request = cloneRequest(input.Request)
	input.Plan = clonePlan(input.Plan)
	input.Results = cloneStepResults(input.Results)
	return input
}

func cloneReplanDecision(decision ReplanDecision) ReplanDecision {
	if decision.Plan != nil {
		plan := clonePlan(*decision.Plan)
		decision.Plan = &plan
	}
	return decision
}

func clonePlan(plan Plan) Plan {
	plan.Steps = append([]Step(nil), plan.Steps...)
	return plan
}

func cloneStepResult(result StepResult) StepResult {
	result.Response = cloneResponse(result.Response)
	return result
}

func cloneStepResults(results []StepResult) []StepResult {
	if results == nil {
		return nil
	}
	cloned := make([]StepResult, len(results))
	for index, result := range results {
		cloned[index] = cloneStepResult(result)
	}
	return cloned
}

func cloneRequest(request agent.Request) agent.Request {
	request.Messages = cloneMessages(request.Messages)
	request.Artifacts = append([]gopact.ArtifactRef(nil), request.Artifacts...)
	request.Metadata = cloneStringMap(request.Metadata)
	return request
}

func cloneResponse(response agent.Response) agent.Response {
	response.Message = cloneMessage(response.Message)
	response.Artifacts = append([]gopact.ArtifactRef(nil), response.Artifacts...)
	response.Metadata = cloneStringMap(response.Metadata)
	return response
}

func cloneMessages(messages []gopact.Message) []gopact.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]gopact.Message, len(messages))
	for index, message := range messages {
		cloned[index] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message gopact.Message) gopact.Message {
	message.Parts = append([]gopact.MessagePart(nil), message.Parts...)
	for index := range message.Parts {
		if message.Parts[index].Ref != nil {
			ref := *message.Parts[index].Ref
			message.Parts[index].Ref = &ref
		}
	}
	return message
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
