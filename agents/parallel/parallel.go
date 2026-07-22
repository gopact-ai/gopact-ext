// Package parallel provides bounded concurrent Agent composition.
package parallel

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

// BranchResult associates one declared child with its response.
type BranchResult struct {
	Name     string         `json:"name"`
	Response agent.Response `json:"response"`
}

// Reducer combines immutable branch results in declaration order.
type Reducer interface {
	Reduce(context.Context, []BranchResult) (agent.Response, error)
}

// ReducerFunc adapts a function into a Reducer.
type ReducerFunc func(context.Context, []BranchResult) (agent.Response, error)

// Reduce implements Reducer.
func (reducer ReducerFunc) Reduce(ctx context.Context, results []BranchResult) (agent.Response, error) {
	if reducer == nil {
		return agent.Response{}, errors.New("parallel: reducer is nil")
	}
	return reducer(ctx, cloneBranchResults(results))
}

// Option configures an Agent during construction.
type Option interface{ apply(*config) }

type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct {
	maxParallelism  int
	workflowOptions []workflow.BuildOption
	validation      *contract.Validator
}

// WithMaxParallelism limits simultaneously executing branches.
func WithMaxParallelism(limit int) Option {
	return optionFunc(func(config *config) {
		config.maxParallelism = limit
		config.validation.Positive("max parallelism", limit)
	})
}

// WithWorkflowOptions configures the underlying Workflow.
func WithWorkflowOptions(options ...workflow.BuildOption) Option {
	return optionFunc(func(config *config) {
		config.workflowOptions = append([]workflow.BuildOption(nil), options...)
	})
}

// Agent invokes one immutable set of child branches through one Workflow.
type Agent struct {
	workflow *agent.WorkflowAgent
}

var _ agent.Agent = (*Agent)(nil)

// New creates an immutable parallel Agent from one Directory snapshot.
func New(identity agent.Identity, directory *agent.Directory, childNames []string, reducer Reducer, options ...Option) (*Agent, error) {
	validator := contract.New("parallel").
		Identity("agent", identity).
		Present("directory", directory).
		Present("reducer", reducer).
		NonEmpty("children", len(childNames))
	for index, name := range childNames {
		validator.Required(fmt.Sprintf("child %d name", index), name).Unique("child", name)
	}
	if err := validator.Err(); err != nil {
		return nil, err
	}
	configuration := config{validation: contract.New("parallel")}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	if err := configuration.validation.Err(); err != nil {
		return nil, err
	}
	children := make([]agent.Agent, len(childNames))
	for index, name := range childNames {
		child, ok := directory.Lookup(name)
		if !ok || contract.IsNil(child) {
			return nil, fmt.Errorf("parallel: child %q is not in the directory", name)
		}
		children[index] = child
	}
	parallelism := configuration.maxParallelism
	if parallelism == 0 {
		parallelism = len(children)
	}
	buildOptions := append([]workflow.BuildOption(nil), configuration.workflowOptions...)
	buildOptions = append(buildOptions, workflow.WithTopologyVersion(identity.Version), workflow.WithMaxParallelism(parallelism))
	wf := workflow.New[agent.Request, agent.Response](identity.Name, buildOptions...)
	plan := wf.Node("plan", func(_ context.Context, request agent.Request) (agent.Request, error) {
		return cloneRequest(request), nil
	})
	nodes := make([]*workflow.Node[agent.Request, agent.Response], len(children))
	for index, child := range children {
		node := wf.AddInvokable("child."+child.Identity().Name, child)
		nodes[index] = node
		wf.Edge(plan, node)
	}
	merge := wf.Merge("merge", func(ctx context.Context, inputs workflow.Inputs) (agent.Response, error) {
		results := make([]BranchResult, len(nodes))
		for index, node := range nodes {
			response, err := inputs.One(node)
			if err != nil {
				return agent.Response{}, err
			}
			results[index] = BranchResult{Name: children[index].Identity().Name, Response: cloneResponse(response)}
		}
		response, err := reducer.Reduce(ctx, results)
		if err != nil {
			return agent.Response{}, fmt.Errorf("parallel: reduce branches: %w", err)
		}
		return cloneResponse(response), nil
	})
	for _, node := range nodes {
		wf.Edge(node, merge)
	}
	plan.Route(func(_ context.Context, request agent.Request) (workflow.Dispatch, error) {
		dispatch := plan.Once(nodes[0], cloneRequest(request))
		for _, node := range nodes[1:] {
			dispatch = dispatch.And(plan.Once(node, cloneRequest(request)))
		}
		return dispatch.WithSettle(workflow.SettleAll()), nil
	})
	wf.Entry(plan)
	wf.Exit(merge)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("parallel: build workflow: %w", err)
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

// Invoke runs all branches and reduces their ordered results.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("parallel: agent is nil")
	}
	return target.workflow.Invoke(ctx, request, options...)
}

func cloneBranchResults(results []BranchResult) []BranchResult {
	if results == nil {
		return nil
	}
	cloned := make([]BranchResult, len(results))
	for index, result := range results {
		result.Response = cloneResponse(result.Response)
		cloned[index] = result
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
	return message.Clone()
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
