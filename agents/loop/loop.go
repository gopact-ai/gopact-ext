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
	maxIterations int
	validation    *contract.Validator
}

// WithMaxIterations sets the positive hard iteration limit.
func WithMaxIterations(limit int) Option {
	return optionFunc(func(config *config) {
		config.maxIterations = limit
		config.validation.Positive("max iterations", limit)
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
	wf := workflow.New[agent.Request, agent.Response](identity.Name, workflow.WithTopologyVersion(identity.Version))
	state := wf.Context(func(request agent.Request) loopContext {
		return loopContext{Request: cloneRequest(request)}
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
			response, err := child.Invoke(ctx, cloneRequest(request), options...)
			if err != nil {
				return agent.Response{}, err
			}
			current.Iteration++
			current.Request = cloneRequest(request)
			if err := state.Set(ctx, current); err != nil {
				return agent.Response{}, err
			}
			return cloneResponse(response), nil
		},
	))
	conditionNode := wf.Node("condition", func(ctx context.Context, response agent.Response) (conditionResult, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return conditionResult{}, err
		}
		decision, err := condition.Evaluate(ctx, Iteration{
			Number: current.Iteration, Request: current.Request, Response: response,
		})
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
		return conditionResult{Decision: decision, Response: cloneResponse(response), Next: next}, nil
	})
	finish := wf.Node("finish", func(_ context.Context, response agent.Response) (agent.Response, error) {
		return cloneResponse(response), nil
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
	return agent.Request{
		Messages: []gopact.Message{cloneMessage(response.Message)}, Artifacts: cloneRefs(response.Artifacts),
		Metadata: cloneStringMap(response.Metadata),
	}
}

func cloneIteration(iteration Iteration) Iteration {
	iteration.Request = cloneRequest(iteration.Request)
	iteration.Response = cloneResponse(iteration.Response)
	return iteration
}

func cloneRequest(request agent.Request) agent.Request {
	request.Messages = cloneMessages(request.Messages)
	request.Artifacts = cloneRefs(request.Artifacts)
	request.Metadata = cloneStringMap(request.Metadata)
	return request
}

func cloneResponse(response agent.Response) agent.Response {
	response.Message = cloneMessage(response.Message)
	response.Artifacts = cloneRefs(response.Artifacts)
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

func cloneRefs(refs []gopact.ArtifactRef) []gopact.ArtifactRef {
	return append([]gopact.ArtifactRef(nil), refs...)
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
