// Package supervisor provides hierarchical multi-agent delegation.
package supervisor

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

const defaultMaxRounds = 32

// DecisionKind is the supervisor's closed decision set.
type DecisionKind string

const (
	DecisionDelegate DecisionKind = "delegate"
	DecisionFinal    DecisionKind = "final"
)

// Decision is one supervisor transition.
type Decision struct {
	Kind     DecisionKind    `json:"kind"`
	Child    string          `json:"child,omitempty"`
	Request  agent.Request   `json:"request,omitempty"`
	Response *agent.Response `json:"response,omitempty"`
	Reason   string          `json:"reason,omitempty"`
}

// DelegationResult records one child result for later evaluation.
type DelegationResult struct {
	Child    string         `json:"child"`
	Request  agent.Request  `json:"request"`
	Response agent.Response `json:"response"`
}

// DecisionInput is passed to the Decider.
type DecisionInput struct {
	Request agent.Request
	Round   int
	Results []DelegationResult
}

// Decider chooses delegation or finalization.
type Decider interface {
	Decide(context.Context, DecisionInput) (Decision, error)
}

// DeciderFunc adapts a function into a Decider.
type DeciderFunc func(context.Context, DecisionInput) (Decision, error)

func (decider DeciderFunc) Decide(ctx context.Context, input DecisionInput) (Decision, error) {
	if decider == nil {
		return Decision{}, errors.New("supervisor: decider is nil")
	}
	decision, err := decider(ctx, cloneDecisionInput(input))
	return cloneDecision(decision), err
}

// Option configures an Agent during construction.
type Option interface{ apply(*config) }
type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct {
	maxRounds  int
	validation *contract.Validator
}

// WithMaxRounds bounds the number of child delegations.
func WithMaxRounds(limit int) Option {
	return optionFunc(func(config *config) {
		config.maxRounds = limit
		config.validation.Positive("max rounds", limit)
	})
}

// State is the Agent-domain delegation context.
type State struct {
	Request agent.Request
	Round   int
	Results []DelegationResult
}

type signal struct{}
type delegation struct {
	Child   string
	Request agent.Request
}

// Agent delegates through one Workflow until the Decider returns a final response.
type Agent struct{ workflow *agent.WorkflowAgent }

var _ agent.Agent = (*Agent)(nil)

// ErrRoundLimit reports a delegation beyond the configured round limit.
var ErrRoundLimit = errors.New("supervisor: round limit reached")

// New creates a supervisor over an immutable child Directory.
func New(identity agent.Identity, directory *agent.Directory, decider Decider, options ...Option) (*Agent, error) {
	if err := contract.New("supervisor").
		Identity("agent", identity).
		Present("directory", directory).
		Present("decider", decider).
		Err(); err != nil {
		return nil, err
	}
	configuration := config{maxRounds: defaultMaxRounds, validation: contract.New("supervisor")}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	if err := configuration.validation.Err(); err != nil {
		return nil, err
	}
	wf := workflow.New[agent.Request, agent.Response](identity.Name, workflow.WithTopologyVersion(identity.Version))
	state := wf.Context(func(request agent.Request) State { return State{Request: cloneRequest(request)} })
	start := wf.Node("start", func(_ context.Context, _ agent.Request) (signal, error) {
		return signal{}, nil
	})
	decide := wf.Merge("decide", func(ctx context.Context, _ workflow.Inputs) (Decision, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return Decision{}, err
		}
		decision, err := decider.Decide(ctx, DecisionInput{
			Request: cloneRequest(current.Request), Round: current.Round + 1,
			Results: cloneDelegationResults(current.Results),
		})
		if err != nil {
			return Decision{}, fmt.Errorf("supervisor: decide: %w", err)
		}
		if err := validateDecision(directory, current.Round, configuration.maxRounds, decision); err != nil {
			return Decision{}, err
		}
		return cloneDecision(decision), nil
	})
	delegate := wf.AddInvokable("delegate", delegationDispatcher{directory: directory})
	record := wf.Node("record", func(ctx context.Context, result DelegationResult) (signal, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return signal{}, err
		}
		current.Results = append(current.Results, cloneDelegationResult(result))
		current.Round++
		if err := state.Set(ctx, current); err != nil {
			return signal{}, err
		}
		return signal{}, nil
	})
	finish := wf.Node("finish", func(_ context.Context, response agent.Response) (agent.Response, error) {
		return cloneResponse(response), nil
	})
	decide.Route(func(_ context.Context, decision Decision) (workflow.Dispatch, error) {
		if decision.Kind == DecisionDelegate {
			return decide.Once(delegate, delegation{Child: decision.Child, Request: cloneRequest(decision.Request)}), nil
		}
		return decide.Once(finish, cloneResponse(*decision.Response)), nil
	})
	wf.Entry(start)
	wf.Edge(start, decide)
	wf.Edge(decide, delegate)
	wf.Edge(decide, finish)
	wf.Edge(delegate, record)
	wf.Edge(record, decide)
	wf.Exit(finish)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("supervisor: build workflow: %w", err)
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

// Invoke delegates until the supervisor returns a final decision.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("supervisor: agent is nil")
	}
	return target.workflow.Invoke(ctx, cloneRequest(request), options...)
}

type delegationDispatcher struct{ directory *agent.Directory }

func (dispatcher delegationDispatcher) Invoke(ctx context.Context, input delegation, options ...gopact.RunOption) (DelegationResult, error) {
	child, ok := dispatcher.directory.Lookup(input.Child)
	if !ok || contract.IsNil(child) {
		return DelegationResult{}, fmt.Errorf("supervisor: child %q is not in the directory", input.Child)
	}
	response, err := child.Invoke(ctx, cloneRequest(input.Request), options...)
	if err != nil {
		return DelegationResult{}, fmt.Errorf("supervisor: delegate %q: %w", input.Child, err)
	}
	return DelegationResult{
		Child: input.Child, Request: cloneRequest(input.Request), Response: cloneResponse(response),
	}, nil
}

func validateDecision(directory *agent.Directory, round, maxRounds int, decision Decision) error {
	if err := decision.validate(); err != nil {
		return err
	}
	if decision.Kind != DecisionDelegate {
		return nil
	}
	if round >= maxRounds {
		return ErrRoundLimit
	}
	child, ok := directory.Lookup(decision.Child)
	if !ok || contract.IsNil(child) {
		return fmt.Errorf("supervisor: child %q is not in the directory", decision.Child)
	}
	return nil
}

func (decision Decision) validate() error {
	validator := contract.New("supervisor")
	switch decision.Kind {
	case DecisionDelegate:
		validator.
			Required("delegate child", decision.Child).
			Check(decision.Response == nil, "delegate decision cannot carry response")
	case DecisionFinal:
		validator.
			Present("final response", decision.Response).
			Check(decision.Child == "", "final decision cannot carry child")
	default:
		validator.Check(false, "unknown decision kind %q", decision.Kind)
	}
	return validator.Err()
}

func cloneDecision(decision Decision) Decision {
	decision.Request = cloneRequest(decision.Request)
	if decision.Response != nil {
		response := cloneResponse(*decision.Response)
		decision.Response = &response
	}
	return decision
}

func cloneDecisionInput(input DecisionInput) DecisionInput {
	input.Request = cloneRequest(input.Request)
	input.Results = cloneDelegationResults(input.Results)
	return input
}

func cloneDelegationResult(result DelegationResult) DelegationResult {
	result.Request = cloneRequest(result.Request)
	result.Response = cloneResponse(result.Response)
	return result
}

func cloneDelegationResults(results []DelegationResult) []DelegationResult {
	if results == nil {
		return nil
	}
	cloned := make([]DelegationResult, len(results))
	for index, result := range results {
		cloned[index] = cloneDelegationResult(result)
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
