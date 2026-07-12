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
	return selector(ctx, cloneRequest(request), append([]agent.Identity(nil), candidates...))
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

// New creates a one-shot router over an immutable Directory snapshot.
func New(identity agent.Identity, directory *agent.Directory, selector Selector) (*Agent, error) {
	if err := contract.New("router").
		Identity("agent", identity).
		Present("directory", directory).
		Present("selector", selector).
		Err(); err != nil {
		return nil, err
	}
	candidates := directory.List()
	wf := workflow.New[agent.Request, agent.Response](identity.Name, workflow.WithTopologyVersion(identity.Version))
	selectNode := wf.Node("select", func(ctx context.Context, request agent.Request) (routeResult, error) {
		selection, err := selector.Select(ctx, cloneRequest(request), candidates)
		if err != nil {
			return routeResult{}, fmt.Errorf("router: select child: %w", err)
		}
		if selection.Child == "" {
			return routeResult{}, errors.New("router: selector returned an empty child")
		}
		if child, ok := directory.Lookup(selection.Child); !ok || contract.IsNil(child) {
			return routeResult{}, fmt.Errorf("router: selected child %q is not in the directory", selection.Child)
		}
		return routeResult{Request: cloneRequest(request), Selection: selection}, nil
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

func cloneRequest(request agent.Request) agent.Request {
	request.Messages = cloneMessages(request.Messages)
	request.Artifacts = append([]gopact.ArtifactRef(nil), request.Artifacts...)
	request.Metadata = cloneStringMap(request.Metadata)
	return request
}

func cloneMessages(messages []gopact.Message) []gopact.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]gopact.Message, len(messages))
	for index, message := range messages {
		message.Parts = append([]gopact.MessagePart(nil), message.Parts...)
		cloned[index] = message
	}
	return cloned
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
