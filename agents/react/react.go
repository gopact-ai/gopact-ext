// Package react provides a model-tool iteration agent.
package react

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

const (
	defaultMaxTurns         = 32
	defaultMaxToolCalls     = 64
	defaultMaxParallelTools = 8
)

// State is the Agent-domain context of one ReAct run.
type State struct {
	Turn                int
	ToolCalls           int
	Messages            []gopact.Message
	PendingObservations []agent.Observation
	RemainingToolCalls  []gopact.ToolCall
	Artifacts           []gopact.ArtifactRef
	Metadata            map[string]string
}

// Limits bound one ReAct run without exposing scheduler metadata.
type Limits struct {
	MaxTurns         int
	MaxToolCalls     int
	MaxParallelTools int
}

// DefaultLimits returns the built-in ReAct safety limits.
func DefaultLimits() Limits {
	return Limits{
		MaxTurns: defaultMaxTurns, MaxToolCalls: defaultMaxToolCalls, MaxParallelTools: defaultMaxParallelTools,
	}
}

// Option configures an Agent during construction.
type Option interface{ apply(*config) }

type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct {
	instruction     string
	tools           []agent.Tool
	limits          Limits
	workflowOptions []workflow.BuildOption
	validation      *contract.Validator
}

// WithInstruction sets the stable system instruction for new runs.
func WithInstruction(instruction string) Option {
	return optionFunc(func(config *config) { config.instruction = instruction })
}

// WithTools replaces the immutable tools available to the Agent.
func WithTools(tools ...agent.Tool) Option {
	return optionFunc(func(config *config) { config.tools = append([]agent.Tool(nil), tools...) })
}

// WithLimits replaces the positive ReAct safety limits.
func WithLimits(limits Limits) Option {
	return optionFunc(func(config *config) {
		config.limits = limits
		config.validation.
			Positive("max turns", limits.MaxTurns).
			Positive("max tool calls", limits.MaxToolCalls).
			Positive("max parallel tools", limits.MaxParallelTools)
	})
}

// WithWorkflowOptions configures the underlying Workflow.
func WithWorkflowOptions(options ...workflow.BuildOption) Option {
	return optionFunc(func(config *config) {
		config.workflowOptions = append([]workflow.BuildOption(nil), options...)
	})
}

type turnKind int

const (
	turnFinal turnKind = iota + 1
	turnTools
	turnRepair
)

type turnResult struct {
	Kind    turnKind
	Message gopact.Message
}

type toolBatch struct{ Calls []gopact.ToolCall }
type toolProgress struct{ More bool }
type continuation struct{ Tools bool }

// Agent iterates between one model and its configured tools through one Workflow.
type Agent struct {
	workflow *agent.WorkflowAgent
}

var _ agent.Agent = (*Agent)(nil)

// New creates an immutable ReAct Agent.
func New(identity agent.Identity, model gopact.Model, options ...Option) (*Agent, error) {
	if err := contract.New("react").Identity("agent", identity).Present("model", model).Err(); err != nil {
		return nil, err
	}
	configuration := config{limits: DefaultLimits(), validation: contract.New("react")}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	if err := configuration.validation.Err(); err != nil {
		return nil, err
	}
	specs, registry, err := indexTools(configuration.tools)
	if err != nil {
		return nil, err
	}
	buildOptions := append([]workflow.BuildOption(nil), configuration.workflowOptions...)
	buildOptions = append(
		buildOptions,
		workflow.WithTopologyVersion(identity.Version),
		workflow.WithMaxParallelism(configuration.limits.MaxParallelTools),
	)
	wf := workflow.New[agent.Request, agent.Response](identity.Name, buildOptions...)
	state := wf.Context(func(request agent.Request) State {
		messages := cloneMessages(request.Messages)
		if configuration.instruction != "" {
			messages = append([]gopact.Message{{
				Role: "system", Parts: []gopact.MessagePart{{Type: "text", Text: configuration.instruction}},
			}}, messages...)
		}
		return State{
			Messages: messages, Artifacts: cloneRefs(request.Artifacts), Metadata: cloneStringMap(request.Metadata),
		}
	})
	prepare := wf.Node("prepare", func(ctx context.Context, _ agent.Request) (gopact.ModelRequest, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return gopact.ModelRequest{}, err
		}
		if current.Turn >= configuration.limits.MaxTurns {
			return gopact.ModelRequest{}, fmt.Errorf("react: max turns %d reached", configuration.limits.MaxTurns)
		}
		current.Turn++
		for _, observation := range current.PendingObservations {
			current.Messages = append(current.Messages, cloneMessage(observation.Message))
		}
		current.PendingObservations = nil
		if err := state.Set(ctx, current); err != nil {
			return gopact.ModelRequest{}, err
		}
		request := model.NewRequest(cloneMessages(current.Messages)...)
		request.Tools = cloneToolSpecs(specs)
		return request, nil
	})
	modelNode := wf.Node("model", func(ctx context.Context, request gopact.ModelRequest) (turnResult, error) {
		response, err := model.Invoke(ctx, request)
		if err != nil {
			return turnResult{}, fmt.Errorf("react: invoke model: %w", err)
		}
		current, err := state.Get(ctx)
		if err != nil {
			return turnResult{}, err
		}
		if len(response.Message.Parts) > 0 || response.Message.Role != "" {
			current.Messages = append(current.Messages, cloneMessage(response.Message))
		}
		result, err := normalizeTurn(response, &current, configuration.limits)
		if err != nil {
			return turnResult{}, err
		}
		if err := state.Set(ctx, current); err != nil {
			return turnResult{}, err
		}
		return result, nil
	})
	dispatchTools := wf.Node("dispatch-tools", func(ctx context.Context, _ agent.Request) (toolBatch, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return toolBatch{}, err
		}
		if len(current.RemainingToolCalls) == 0 {
			return toolBatch{}, errors.New("react: no pending tool calls")
		}
		end := toolBatchEnd(registry, current.RemainingToolCalls)
		batch := cloneToolCalls(current.RemainingToolCalls[:end])
		current.RemainingToolCalls = cloneToolCalls(current.RemainingToolCalls[end:])
		if err := state.Set(ctx, current); err != nil {
			return toolBatch{}, err
		}
		return toolBatch{Calls: batch}, nil
	})
	toolNode := wf.AddInvokable("tool", toolDispatcher{registry: registry})
	observe := wf.Merge("observe-tools", func(ctx context.Context, inputs workflow.Inputs) (toolProgress, error) {
		outcomes, err := inputs.All(toolNode)
		if err != nil {
			return toolProgress{}, err
		}
		current, err := state.Get(ctx)
		if err != nil {
			return toolProgress{}, err
		}
		for _, outcome := range outcomes {
			if err := current.recordToolOutcome(outcome); err != nil {
				return toolProgress{}, fmt.Errorf("react: observe tool outcome: %w", err)
			}
		}
		if err := state.Set(ctx, current); err != nil {
			return toolProgress{}, err
		}
		return toolProgress{More: len(current.RemainingToolCalls) > 0}, nil
	})
	finish := wf.Node("finish", func(ctx context.Context, message gopact.Message) (agent.Response, error) {
		if len(message.Parts) == 0 {
			return agent.Response{}, errors.New("react: final intent has no message")
		}
		current, err := state.Get(ctx)
		if err != nil {
			return agent.Response{}, err
		}
		return agent.Response{
			Message: cloneMessage(message), Artifacts: cloneRefs(current.Artifacts), Metadata: cloneStringMap(current.Metadata),
		}, nil
	})
	continueNode := wf.Merge("continue", func(_ context.Context, inputs workflow.Inputs) (continuation, error) {
		if turn, ok, err := inputs.Lookup(modelNode); err != nil {
			return continuation{}, err
		} else if ok {
			return continuation{Tools: turn.Kind == turnTools}, nil
		}
		progress, err := inputs.One(observe)
		if err != nil {
			return continuation{}, err
		}
		return continuation{Tools: progress.More}, nil
	})
	modelNode.Route(func(_ context.Context, result turnResult) (workflow.Dispatch, error) {
		switch result.Kind {
		case turnFinal:
			return modelNode.Once(finish, result.Message), nil
		case turnTools, turnRepair:
			return modelNode.To(continueNode), nil
		default:
			return workflow.Dispatch{}, fmt.Errorf("react: unsupported turn kind %d", result.Kind)
		}
	})
	dispatchTools.Route(func(_ context.Context, batch toolBatch) (workflow.Dispatch, error) {
		return dispatchTools.Each(toolNode, cloneToolCalls(batch.Calls)...).WithSettle(workflow.SettleAll()), nil
	})
	continueNode.Route(func(_ context.Context, next continuation) (workflow.Dispatch, error) {
		if next.Tools {
			return continueNode.Once(dispatchTools, agent.Request{}), nil
		}
		return continueNode.Once(prepare, agent.Request{}), nil
	})
	wf.Entry(prepare)
	wf.Edge(prepare, modelNode)
	wf.Edge(modelNode, continueNode)
	wf.Edge(modelNode, finish)
	wf.Edge(continueNode, prepare)
	wf.Edge(continueNode, dispatchTools)
	wf.Edge(dispatchTools, toolNode)
	wf.Edge(toolNode, observe)
	wf.Edge(observe, continueNode)
	wf.Exit(finish)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("react: build workflow: %w", err)
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

// Invoke runs the model-tool loop.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("react: agent is nil")
	}
	return target.workflow.Invoke(ctx, request, options...)
}

func normalizeTurn(response gopact.ModelResponse, state *State, limits Limits) (turnResult, error) {
	switch intent := response.Intent.(type) {
	case gopact.FinalIntent:
		return turnResult{Kind: turnFinal, Message: cloneMessage(response.Message)}, nil
	case *gopact.FinalIntent:
		return normalizeFinalPointer(intent, response.Message)
	case gopact.ToolCallIntent:
		return normalizeToolTurn(intent.Calls, state, limits)
	case *gopact.ToolCallIntent:
		return normalizeToolPointer(intent, state, limits)
	case gopact.RepairIntent:
		return normalizeRepairTurn(intent.Repair, state)
	case *gopact.RepairIntent:
		return normalizeRepairPointer(intent, state)
	case gopact.RefusalIntent:
		return turnResult{}, refusalError(intent.Refusal)
	case *gopact.RefusalIntent:
		return normalizeRefusalPointer(intent)
	case nil:
		return turnResult{}, errors.New("react: model response has no intent")
	default:
		return turnResult{}, fmt.Errorf("react: unsupported model intent %T", response.Intent)
	}
}

func normalizeFinalPointer(intent *gopact.FinalIntent, message gopact.Message) (turnResult, error) {
	if intent == nil {
		return turnResult{}, errors.New("react: nil final intent")
	}
	return turnResult{Kind: turnFinal, Message: cloneMessage(message)}, nil
}

func normalizeToolPointer(intent *gopact.ToolCallIntent, state *State, limits Limits) (turnResult, error) {
	if intent == nil {
		return turnResult{}, errors.New("react: nil tool-call intent")
	}
	return normalizeToolTurn(intent.Calls, state, limits)
}

func normalizeRepairPointer(intent *gopact.RepairIntent, state *State) (turnResult, error) {
	if intent == nil {
		return turnResult{}, errors.New("react: nil repair intent")
	}
	return normalizeRepairTurn(intent.Repair, state)
}

func normalizeRefusalPointer(intent *gopact.RefusalIntent) (turnResult, error) {
	if intent == nil {
		return turnResult{}, errors.New("react: nil refusal intent")
	}
	return turnResult{}, refusalError(intent.Refusal)
}

func normalizeToolTurn(calls []gopact.ToolCall, state *State, limits Limits) (turnResult, error) {
	if len(calls) == 0 {
		return turnResult{}, errors.New("react: tool-call intent has no calls")
	}
	if err := validateToolCalls(calls); err != nil {
		return turnResult{}, err
	}
	if len(calls) > limits.MaxToolCalls-state.ToolCalls {
		return turnResult{}, fmt.Errorf("react: max tool calls %d reached", limits.MaxToolCalls)
	}
	state.ToolCalls += len(calls)
	state.RemainingToolCalls = cloneToolCalls(calls)
	return turnResult{Kind: turnTools}, nil
}

func toolBatchEnd(registry map[string]agent.Tool, calls []gopact.ToolCall) int {
	if _, invokable := registry[calls[0].Name].(agent.InvokableTool); invokable {
		return 1
	}
	end := 1
	for end < len(calls) {
		if _, invokable := registry[calls[end].Name].(agent.InvokableTool); invokable {
			break
		}
		end++
	}
	return end
}

func normalizeRepairTurn(repair gopact.RepairRequest, state *State) (turnResult, error) {
	observation, err := agent.ObserveRepairRequest(fmt.Sprintf("repair-%d", state.Turn), repair)
	if err != nil {
		return turnResult{}, fmt.Errorf("react: observe repair request: %w", err)
	}
	state.PendingObservations = append(state.PendingObservations, observation)
	return turnResult{Kind: turnRepair}, nil
}

func refusalError(refusal gopact.Refusal) error {
	reason := refusal.Reason
	if reason == "" {
		reason = "model refused"
	}
	return fmt.Errorf("react: model refusal: %s", reason)
}

type toolDispatcher struct {
	registry map[string]agent.Tool
}

func (dispatcher toolDispatcher) Invoke(ctx context.Context, call gopact.ToolCall, options ...gopact.RunOption) (gopact.ToolOutcome, error) {
	tool, exists := dispatcher.registry[call.Name]
	if !exists {
		return unknownToolOutcome(call), nil
	}
	var outcome gopact.ToolOutcome
	var err error
	switch typed := tool.(type) {
	case agent.InvokableTool:
		outcome, err = typed.Invoke(ctx, cloneToolCall(call), options...)
	case agent.DirectTool:
		outcome, err = typed.ExecuteTool(ctx, cloneToolCall(call))
	default:
		return nil, fmt.Errorf("react: tool %q is not executable", call.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("react: execute tool %q: %w", call.Name, err)
	}
	if outcome == nil || outcome.ToolCallID() != call.ID || outcome.ToolName() != call.Name {
		return nil, fmt.Errorf("react: tool %q returned mismatched outcome identity", call.Name)
	}
	if _, interrupted := asToolInterrupt(outcome); interrupted {
		return nil, fmt.Errorf("react: tool %q returned an interrupt without a Workflow continuation", call.Name)
	}
	return outcome, nil
}

func unknownToolOutcome(call gopact.ToolCall) gopact.ToolOutcome {
	return gopact.ToolRejectedOutcome{
		CallID: call.ID, Name: call.Name,
		Rejection: gopact.ToolRejection{
			Reason: "unknown_tool", Message: "unknown tool: " + call.Name,
			RetryHint: &gopact.RetryHint{Retryable: true, Message: "choose one of the advertised tools"},
		},
	}
}

func validateToolCalls(calls []gopact.ToolCall) error {
	validator := contract.New("react").NonEmpty("tool calls", len(calls))
	for index, call := range calls {
		validator.
			Required(fmt.Sprintf("tool call %d id", index), call.ID).
			Required(fmt.Sprintf("tool call %d name", index), call.Name).
			OptionalJSON(fmt.Sprintf("tool call %q arguments", call.ID), call.Arguments).
			Unique("tool call id", call.ID)
	}
	return validator.Err()
}

func indexTools(tools []agent.Tool) ([]gopact.ToolSpec, map[string]agent.Tool, error) {
	type indexedTool struct {
		tool agent.Tool
		spec gopact.ToolSpec
	}
	indexed := make([]indexedTool, 0, len(tools))
	validator := contract.New("react")
	for index, tool := range tools {
		validator.Present(fmt.Sprintf("tool %d", index), tool)
		if contract.IsNil(tool) {
			continue
		}
		spec := cloneToolSpec(tool.Spec())
		_, direct := tool.(agent.DirectTool)
		_, invokable := tool.(agent.InvokableTool)
		validator.
			Required(fmt.Sprintf("tool %d name", index), spec.Name).
			OptionalJSON(fmt.Sprintf("tool %q schema", spec.Name), spec.Schema).
			Unique("tool", spec.Name).
			Check(direct || invokable, "tool %q is not executable", spec.Name)
		indexed = append(indexed, indexedTool{tool: tool, spec: spec})
	}
	if err := validator.Err(); err != nil {
		return nil, nil, err
	}
	specs := make([]gopact.ToolSpec, 0, len(indexed))
	registry := make(map[string]agent.Tool, len(indexed))
	for _, entry := range indexed {
		registry[entry.spec.Name] = entry.tool
		specs = append(specs, entry.spec)
	}
	return specs, registry, nil
}

func (state *State) recordToolOutcome(outcome gopact.ToolOutcome) error {
	observation, err := agent.ObserveToolOutcome(outcome)
	if err != nil {
		return err
	}
	state.PendingObservations = append(state.PendingObservations, observation)
	state.Artifacts = append(state.Artifacts, toolOutcomeRefs(outcome)...)
	return nil
}

func toolOutcomeRefs(outcome gopact.ToolOutcome) []gopact.ArtifactRef {
	var refs []gopact.ArtifactRef
	switch value := outcome.(type) {
	case gopact.ToolResultOutcome:
		refs = append(refs, value.Result.ArtifactRefs...)
		refs = append(refs, value.Result.EffectRefs...)
	case *gopact.ToolResultOutcome:
		if value != nil {
			refs = append(refs, value.Result.ArtifactRefs...)
			refs = append(refs, value.Result.EffectRefs...)
		}
	case gopact.ToolErrorOutcome:
		refs = append(refs, value.Error.PartialRefs...)
	case *gopact.ToolErrorOutcome:
		if value != nil {
			refs = append(refs, value.Error.PartialRefs...)
		}
	}
	return cloneRefs(refs)
}

func asToolInterrupt(outcome gopact.ToolOutcome) (gopact.ToolInterruptOutcome, bool) {
	switch value := outcome.(type) {
	case gopact.ToolInterruptOutcome:
		return value, true
	case *gopact.ToolInterruptOutcome:
		if value != nil {
			return *value, true
		}
	}
	return gopact.ToolInterruptOutcome{}, false
}

func cloneToolCalls(calls []gopact.ToolCall) []gopact.ToolCall {
	if calls == nil {
		return nil
	}
	cloned := make([]gopact.ToolCall, len(calls))
	for index, call := range calls {
		cloned[index] = cloneToolCall(call)
	}
	return cloned
}

func cloneToolCall(call gopact.ToolCall) gopact.ToolCall {
	call.Arguments = append([]byte(nil), call.Arguments...)
	return call
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

func cloneToolSpecs(specs []gopact.ToolSpec) []gopact.ToolSpec {
	if specs == nil {
		return nil
	}
	cloned := make([]gopact.ToolSpec, len(specs))
	for index, spec := range specs {
		cloned[index] = cloneToolSpec(spec)
	}
	return cloned
}

func cloneToolSpec(spec gopact.ToolSpec) gopact.ToolSpec {
	spec.Schema = append([]byte(nil), spec.Schema...)
	spec.Metadata = cloneStringMap(spec.Metadata)
	return spec
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
