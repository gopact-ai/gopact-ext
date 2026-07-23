// Package loop provides bounded conditional repetition of one child Agent.
package loop

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

const defaultMaxIterations = 32

var ErrMaxIterations = errors.New("loop: max iterations reached")

// Decision is one typed loop condition result.
type Decision int

// Loop decisions.
const (
	DecisionUnknown Decision = iota
	DecisionContinue
	DecisionStop
)

// Iteration is the immutable input to a Condition.
type Iteration struct {
	Number   int
	Request  agent.Request
	Response agent.Response
}

// Condition decides whether the completed child should run again.
type Condition interface {
	Evaluate(context.Context, Iteration) (Decision, error)
}

// ConditionFunc adapts a function into a Condition.
type ConditionFunc func(context.Context, Iteration) (Decision, error)

// Evaluate implements Condition.
func (condition ConditionFunc) Evaluate(ctx context.Context, iteration Iteration) (Decision, error) {
	if condition == nil {
		return DecisionUnknown, errors.New("loop: condition is nil")
	}
	return condition(ctx, cloneIteration(iteration))
}

// Option configures an Agent during construction.
type Option interface{ apply(*config) }

type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct {
	maxIterations   int
	workflowOptions []workflow.BuildOption
	validation      *contract.Validator
}

// WithMaxIterations sets the positive hard iteration limit.
func WithMaxIterations(limit int) Option {
	return optionFunc(func(config *config) {
		config.maxIterations = limit
		config.validation.Positive("max iterations", limit)
	})
}

// WithWorkflowOptions configures the underlying Workflow.
func WithWorkflowOptions(options ...workflow.BuildOption) Option {
	return optionFunc(func(config *config) {
		config.workflowOptions = append([]workflow.BuildOption(nil), options...)
	})
}

type loopContext struct {
	Iteration int
	Request   agent.Request
}

type conditionResult struct {
	Decision Decision
	Response agent.Response
	Next     agent.Request
}

// Agent conditionally repeats one immutable child through one Workflow.
type Agent struct {
	workflow *agent.WorkflowAgent
}

var _ agent.Agent = (*Agent)(nil)

// New creates an immutable loop Agent.
func New(identity agent.Identity, child agent.Agent, condition Condition, options ...Option) (*Agent, error) {
	if err := contract.New("loop").
		Identity("agent", identity).
		Present("child", child).
		Present("condition", condition).
		Err(); err != nil {
		return nil, err
	}
	if err := contract.New("loop").Identity("child", child.Identity()).Err(); err != nil {
		return nil, err
	}
	configuration := config{maxIterations: defaultMaxIterations, validation: contract.New("loop")}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	if err := configuration.validation.Err(); err != nil {
		return nil, err
	}
	buildOptions := append([]workflow.BuildOption(nil), configuration.workflowOptions...)
	buildOptions = append(buildOptions, workflow.WithTopologyVersion(identity.Version))
	wf := workflow.New[agent.Request, agent.Response](identity.Name, buildOptions...)
	state := wf.Context(func(request agent.Request) loopContext {
		return loopContext{Request: request.Clone()}
	})
	childNode := wf.AddInvokable("child."+child.Identity().Name, gopact.InvokableFunc[agent.Request, agent.Response](
		func(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
			current, err := state.Get(ctx)
			if err != nil {
				return agent.Response{}, err
			}
			if current.Iteration >= configuration.maxIterations {
				return agent.Response{}, ErrMaxIterations
			}
			response, err := child.Invoke(ctx, request.Clone(), options...)
			if err != nil {
				return agent.Response{}, err
			}
			current.Iteration++
			current.Request = request.Clone()
			if err := state.Set(ctx, current); err != nil {
				return agent.Response{}, err
			}
			return response.Clone(), nil
		},
	))
	conditionNode := wf.Node("condition", func(ctx context.Context, response agent.Response) (conditionResult, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return conditionResult{}, err
		}
		decision, err := condition.Evaluate(ctx, cloneIteration(Iteration{
			Number: current.Iteration, Request: current.Request, Response: response,
		}))
		if err != nil {
			return conditionResult{}, fmt.Errorf("loop: evaluate condition: %w", err)
		}
		if decision != DecisionStop && decision != DecisionContinue {
			return conditionResult{}, fmt.Errorf("loop: unsupported condition decision %d", decision)
		}
		if decision == DecisionContinue && current.Iteration >= configuration.maxIterations {
			return conditionResult{}, ErrMaxIterations
		}
		next := requestFromResponse(response)
		return conditionResult{Decision: decision, Response: response.Clone(), Next: next}, nil
	})
	finish := wf.Node("finish", func(_ context.Context, response agent.Response) (agent.Response, error) {
		return response.Clone(), nil
	})
	conditionNode.Route(func(_ context.Context, result conditionResult) (workflow.Dispatch, error) {
		if result.Decision == DecisionContinue {
			return conditionNode.Once(childNode, result.Next), nil
		}
		return conditionNode.Once(finish, result.Response), nil
	})
	wf.Entry(childNode)
	wf.Edge(childNode, conditionNode)
	wf.Edge(conditionNode, childNode)
	wf.Edge(conditionNode, finish)
	wf.Exit(finish)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("loop: build workflow: %w", err)
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

// Invoke repeats the child until the Condition stops or the hard limit is reached.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("loop: agent is nil")
	}
	return target.workflow.Invoke(ctx, request, options...)
}

func requestFromResponse(response agent.Response) agent.Request {
	response = response.Clone()
	return agent.Request{
		Messages: []gopact.Message{response.Message}, Artifacts: response.Artifacts,
		Metadata: response.Metadata,
	}
}

func cloneIteration(iteration Iteration) Iteration {
	iteration.Request = iteration.Request.Clone()
	iteration.Response = iteration.Response.Clone()
	return iteration
}
