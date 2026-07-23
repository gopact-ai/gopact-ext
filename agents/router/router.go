// Package router provides one-shot child selection and invocation.
package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

// Selection is the immutable result of one routing decision.
type Selection struct {
	Child  string `json:"child"`
	Reason string `json:"reason,omitempty"`
}

// Selector chooses one child from the declared candidates.
type Selector interface {
	Select(context.Context, agent.Request, []agent.Identity) (Selection, error)
}

// SelectorFunc adapts a function into a Selector.
type SelectorFunc func(context.Context, agent.Request, []agent.Identity) (Selection, error)

// Select implements Selector.
func (selector SelectorFunc) Select(ctx context.Context, request agent.Request, candidates []agent.Identity) (Selection, error) {
	if selector == nil {
		return Selection{}, errors.New("router: selector is nil")
	}
	return selector(ctx, request.Clone(), append([]agent.Identity(nil), candidates...))
}

type routeResult struct {
	Request   agent.Request `json:"request"`
	Selection Selection     `json:"selection"`
}

// Agent selects and invokes one immutable child through one Workflow.
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

// New creates a one-shot router over an immutable Directory snapshot.
func New(identity agent.Identity, directory *agent.Directory, selector Selector, options ...Option) (*Agent, error) {
	if err := contract.New("router").
		Identity("agent", identity).
		Present("directory", directory).
		Present("selector", selector).
		Err(); err != nil {
		return nil, err
	}
	configuration := config{}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	candidates := directory.List()
	buildOptions := append([]workflow.BuildOption(nil), configuration.workflowOptions...)
	buildOptions = append(buildOptions, workflow.WithTopologyVersion(identity.Version))
	wf := workflow.New[agent.Request, agent.Response](identity.Name, buildOptions...)
	selectNode := wf.Node("select", func(ctx context.Context, request agent.Request) (routeResult, error) {
		selection, err := selector.Select(ctx, request.Clone(), candidates)
		if err != nil {
			return routeResult{}, fmt.Errorf("router: select child: %w", err)
		}
		if selection.Child == "" {
			return routeResult{}, errors.New("router: selector returned an empty child")
		}
		if child, ok := directory.Lookup(selection.Child); !ok || contract.IsNil(child) {
			return routeResult{}, fmt.Errorf("router: selected child %q is not in the directory", selection.Child)
		}
		return routeResult{Request: request.Clone(), Selection: selection}, nil
	})
	children := make(map[string]*workflow.Node[agent.Request, agent.Response], len(candidates))
	for _, candidate := range candidates {
		child, _ := directory.Lookup(candidate.Name)
		node := wf.AddInvokable("child."+candidate.Name, child)
		children[candidate.Name] = node
		wf.Edge(selectNode, node)
		wf.Exit(node)
	}
	selectNode.Route(func(_ context.Context, result routeResult) (workflow.Dispatch, error) {
		return selectNode.Once(children[result.Selection.Child], result.Request), nil
	})
	wf.Entry(selectNode)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("router: build workflow: %w", err)
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

// Invoke selects one child and returns its response.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("router: agent is nil")
	}
	return target.workflow.Invoke(ctx, request, options...)
}
