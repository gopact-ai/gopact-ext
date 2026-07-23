// Package deep provides long-horizon task orchestration.
package deep

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

const defaultMaxTasks = 32

// TaskStatus is the status of one long-horizon task.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskCompleted TaskStatus = "completed"
)

// Task is one delegated unit of work.
type Task struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	AgentName   string     `json:"agent_name"`
	Status      TaskStatus `json:"status"`
}

// TaskResult stores one completed child result.
type TaskResult struct {
	Task     Task           `json:"task"`
	Response agent.Response `json:"response"`
}

// Progress records a user-visible long-task milestone.
type Progress struct {
	TaskID    string               `json:"task_id"`
	Summary   string               `json:"summary"`
	Artifacts []gopact.ArtifactRef `json:"artifacts,omitempty"`
}

// PlanInput is passed to a Planner.
type PlanInput struct {
	Request agent.Request
	Results []TaskResult
}

// Planner creates the task list.
type Planner interface {
	Plan(context.Context, PlanInput) ([]Task, error)
}

// PlannerFunc adapts a function into a Planner.
type PlannerFunc func(context.Context, PlanInput) ([]Task, error)

func (planner PlannerFunc) Plan(ctx context.Context, input PlanInput) ([]Task, error) {
	if planner == nil {
		return nil, errors.New("deep: planner is nil")
	}
	tasks, err := planner(ctx, clonePlanInput(input))
	return append([]Task(nil), tasks...), err
}

// Option configures an Agent during construction.
type Option interface{ apply(*config) }
type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct {
	maxTasks        int
	workflowOptions []workflow.BuildOption
	validation      *contract.Validator
}

// WithMaxTasks bounds the planned task count.
func WithMaxTasks(limit int) Option {
	return optionFunc(func(config *config) {
		config.maxTasks = limit
		config.validation.Positive("max tasks", limit)
	})
}

// WithWorkflowOptions configures the underlying Workflow.
func WithWorkflowOptions(options ...workflow.BuildOption) Option {
	return optionFunc(func(config *config) {
		config.workflowOptions = append([]workflow.BuildOption(nil), options...)
	})
}

// State is the Agent-domain long-task context.
type State struct {
	Request     agent.Request        `json:"request"`
	Tasks       []Task               `json:"tasks"`
	Next        int                  `json:"next"`
	Results     []TaskResult         `json:"results"`
	Progress    []Progress           `json:"progress"`
	ContextRefs []gopact.ArtifactRef `json:"context_refs,omitempty"`
}

type control struct{ Done bool }
type taskInvocation struct {
	Task    Task
	Request agent.Request
}

// Agent executes a task plan through one Workflow.
type Agent struct{ workflow *agent.WorkflowAgent }

var _ agent.Agent = (*Agent)(nil)

// New creates a Deep Agent over an immutable child Directory.
func New(identity agent.Identity, directory *agent.Directory, planner Planner, options ...Option) (*Agent, error) {
	if err := contract.New("deep").
		Identity("agent", identity).
		Present("directory", directory).
		Present("planner", planner).
		Err(); err != nil {
		return nil, err
	}
	configuration := config{maxTasks: defaultMaxTasks, validation: contract.New("deep")}
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
	state := wf.Context(func(request agent.Request) State { return State{Request: cloneRequest(request)} })
	plan := wf.Node("plan", func(ctx context.Context, request agent.Request) ([]Task, error) {
		tasks, err := planner.Plan(ctx, PlanInput{Request: cloneRequest(request)})
		if err != nil {
			return nil, fmt.Errorf("deep: create task plan: %w", err)
		}
		return tasks, nil
	})
	accept := wf.Node("accept-plan", func(ctx context.Context, tasks []Task) (control, error) {
		accepted, err := acceptTasks(directory, configuration.maxTasks, tasks)
		if err != nil {
			return control{}, err
		}
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		current.Tasks = accepted
		if err := state.Set(ctx, current); err != nil {
			return control{}, err
		}
		return control{}, nil
	})
	next := wf.Merge("continue", func(ctx context.Context, _ workflow.Inputs) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		return control{Done: current.Next >= len(current.Tasks)}, nil
	})
	buildContext := wf.Node("build-context", func(ctx context.Context, _ control) (taskInvocation, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return taskInvocation{}, err
		}
		if current.Next >= len(current.Tasks) {
			return taskInvocation{}, errors.New("deep: task cursor is exhausted")
		}
		request := agent.Request{
			Messages: cloneMessages(current.Request.Messages), Artifacts: cloneRefs(current.ContextRefs),
			Metadata: cloneStringMap(current.Request.Metadata),
		}
		return taskInvocation{Task: current.Tasks[current.Next], Request: request}, nil
	})
	execute := wf.AddInvokable("execute-task", taskDispatcher{directory: directory})
	record := wf.Node("record-task", func(ctx context.Context, result TaskResult) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		if current.Next >= len(current.Tasks) || current.Tasks[current.Next].ID != result.Task.ID {
			return control{}, errors.New("deep: completed task does not match plan cursor")
		}
		current.Tasks[current.Next] = result.Task
		current.Results = append(current.Results, cloneTaskResult(result))
		current.Progress = append(current.Progress, Progress{
			TaskID: result.Task.ID, Summary: result.Task.Description, Artifacts: cloneRefs(result.Response.Artifacts),
		})
		current.ContextRefs = append(current.ContextRefs, cloneRefs(result.Response.Artifacts)...)
		current.Next++
		if err := state.Set(ctx, current); err != nil {
			return control{}, err
		}
		return control{}, nil
	})
	finish := wf.Node("finish", func(ctx context.Context, _ control) (agent.Response, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return agent.Response{}, err
		}
		if len(current.Results) == 0 {
			return agent.Response{}, errors.New("deep: no task result")
		}
		return cloneResponse(current.Results[len(current.Results)-1].Response), nil
	})
	next.Route(func(_ context.Context, current control) (workflow.Dispatch, error) {
		if current.Done {
			return next.Once(finish, control{}), nil
		}
		return next.Once(buildContext, control{}), nil
	})
	wf.Entry(plan)
	wf.Edge(plan, accept)
	wf.Edge(accept, next)
	wf.Edge(next, buildContext)
	wf.Edge(next, finish)
	wf.Edge(buildContext, execute)
	wf.Edge(execute, record)
	wf.Edge(record, next)
	wf.Exit(finish)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("deep: build workflow: %w", err)
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

// Invoke executes the planned long-horizon task set.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("deep: agent is nil")
	}
	return target.workflow.Invoke(ctx, request, options...)
}

type taskDispatcher struct{ directory *agent.Directory }

func (dispatcher taskDispatcher) Invoke(ctx context.Context, input taskInvocation, options ...gopact.RunOption) (TaskResult, error) {
	child, ok := dispatcher.directory.Lookup(input.Task.AgentName)
	if !ok || contract.IsNil(child) {
		return TaskResult{}, fmt.Errorf("deep: task %q child %q is not in the directory", input.Task.ID, input.Task.AgentName)
	}
	response, err := child.Invoke(ctx, cloneRequest(input.Request), options...)
	if err != nil {
		return TaskResult{}, fmt.Errorf("deep: execute task %q: %w", input.Task.ID, err)
	}
	task := input.Task
	task.Status = TaskCompleted
	return TaskResult{Task: task, Response: cloneResponse(response)}, nil
}

func validateTasks(tasks []Task) error {
	validator := contract.New("deep").NonEmpty("tasks", len(tasks))
	for index, task := range tasks {
		validator.
			Required(fmt.Sprintf("task %d id", index), task.ID).
			Required(fmt.Sprintf("task %d description", index), task.Description).
			Required(fmt.Sprintf("task %d agent", index), task.AgentName).
			Check(task.Status == "" || task.Status == TaskPending, "task %d status %q is owned by the runtime", index, task.Status).
			Unique("task", task.ID)
	}
	return validator.Err()
}

func acceptTasks(directory *agent.Directory, maxTasks int, tasks []Task) ([]Task, error) {
	tasks = append([]Task(nil), tasks...)
	structureErr := validateTasks(tasks)
	validator := contract.New("deep").Check(len(tasks) <= maxTasks, "task count %d exceeds %d", len(tasks), maxTasks)
	for index, task := range tasks {
		child, exists := directory.Lookup(task.AgentName)
		validator.Check(exists && !contract.IsNil(child), "task %d child %q is not in the directory", index, task.AgentName)
		tasks[index].Status = TaskPending
	}
	if err := errors.Join(structureErr, validator.Err()); err != nil {
		return nil, err
	}
	return tasks, nil
}

func clonePlanInput(input PlanInput) PlanInput {
	input.Request = cloneRequest(input.Request)
	input.Results = cloneTaskResults(input.Results)
	return input
}

func cloneTaskResult(result TaskResult) TaskResult {
	result.Response = cloneResponse(result.Response)
	return result
}

func cloneTaskResults(results []TaskResult) []TaskResult {
	if results == nil {
		return nil
	}
	cloned := make([]TaskResult, len(results))
	for index, result := range results {
		cloned[index] = cloneTaskResult(result)
	}
	return cloned
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
	return message.Clone()
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
