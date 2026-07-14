// Package sequential provides deterministic ordered Agent composition.
package sequential

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

// Agent invokes a fixed child sequence through one Workflow.
type Agent struct {
	workflow *agent.WorkflowAgent
}

var _ agent.Agent = (*Agent)(nil)

// Option configures an Agent during construction.
type Option interface{ apply(*config) }

type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct{ workflowOptions []workflow.BuildOption }

// WithWorkflowOptions configures the underlying Workflow.
func WithWorkflowOptions(options ...workflow.BuildOption) Option {
	return optionFunc(func(config *config) {
		config.workflowOptions = append([]workflow.BuildOption(nil), options...)
	})
}

// New creates an immutable sequential Agent from one Directory snapshot.
func New(identity agent.Identity, directory *agent.Directory, childNames []string, options ...Option) (*Agent, error) {
	validator := contract.New("sequential").
		Identity("agent", identity).
		Present("directory", directory).
		NonEmpty("children", len(childNames))
	for index, name := range childNames {
		validator.Required(fmt.Sprintf("child %d name", index), name)
	}
	if err := validator.Err(); err != nil {
		return nil, err
	}
	configuration := config{}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	children := make([]agent.Agent, len(childNames))
	for index, name := range childNames {
		child, ok := directory.Lookup(name)
		if !ok || contract.IsNil(child) {
			return nil, fmt.Errorf("sequential: child %q is not in the directory", name)
		}
		children[index] = child
	}
	buildOptions := append([]workflow.BuildOption(nil), configuration.workflowOptions...)
	buildOptions = append(buildOptions, workflow.WithTopologyVersion(identity.Version))
	wf := workflow.New[agent.Request, agent.Response](identity.Name, buildOptions...)
	nodes := make([]*workflow.Node[agent.Request, agent.Response], len(children))
	for index, child := range children {
		nodes[index] = wf.AddInvokable(child.Identity().Name, child)
	}
	for index := range len(nodes) - 1 {
		current, next := nodes[index], nodes[index+1]
		current.Route(func(_ context.Context, response agent.Response) (workflow.Dispatch, error) {
			return current.Once(next, requestFromResponse(response)), nil
		})
		wf.Edge(current, next)
	}
	wf.Entry(nodes[0])
	wf.Exit(nodes[len(nodes)-1])
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("sequential: build workflow: %w", err)
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

// Invoke runs the fixed child sequence.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("sequential: agent is nil")
	}
	return target.workflow.Invoke(ctx, request, options...)
}

func requestFromResponse(response agent.Response) agent.Request {
	return agent.Request{
		Messages:  []gopact.Message{cloneMessage(response.Message)},
		Artifacts: append([]gopact.ArtifactRef(nil), response.Artifacts...),
		Metadata:  cloneStringMap(response.Metadata),
	}
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
