package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/agenttool"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/workflow"
)

func TestAgentReturnsFinalResponseAndConforms(t *testing.T) {
	model := finalModel("done")
	target, err := New(testIdentity(), model)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("work")},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if response.Message.Parts[0].Text != "done" {
		t.Fatalf("response = %+v, want done", response)
	}
	gopacttest.RequireAgentConformance(t, gopacttest.AgentConformanceCase{
		Agent:   target,
		Request: agent.Request{Messages: []gopact.Message{gopact.UserMessage("work")}},
		Validate: func(response agent.Response) error {
			if len(response.Message.Parts) != 1 || response.Message.Parts[0].Text != "done" {
				return errors.New("unexpected final response")
			}
			return nil
		},
	})
}

func TestAgentUsesWorkflowTurnFacts(t *testing.T) {
	target, err := New(testIdentity(), finalModel("done"))
	if err != nil {
		t.Fatal(err)
	}
	var completed []string
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("react-workflow"), gopact.WithStrictEventHandler(
		func(_ context.Context, event gopact.Event) error {
			if event.RunID == "react-workflow" && event.Type == workflow.EventNodeCompleted {
				completed = append(completed, event.NodeID)
			}
			return nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(completed, ","), "prepare,model,finish"; got != want {
		t.Fatalf("completed nodes = %q, want Workflow turn facts %q", got, want)
	}
}

func TestAgentExecutesMultipleToolsAndReturnsStableFeedback(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{
		{
			Message: gopact.Message{Role: "assistant"},
			Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{
				{ID: "call-1", Name: "slow"},
				{ID: "call-2", Name: "fast"},
			}},
		},
		finalResponse("done"),
	}}
	fastStarted := make(chan struct{})
	slow := directTool("slow", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		<-fastStarted
		return resultOutcome(call, "slow-result"), nil
	})
	fast := directTool("fast", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		close(fastStarted)
		return resultOutcome(call, "fast-result"), nil
	})
	target, err := New(testIdentity(), model, WithTools(slow, fast))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("work")},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if response.Message.Parts[0].Text != "done" {
		t.Fatalf("response = %+v", response)
	}
	requests := model.Requests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	last := requests[1].Messages
	if len(last) < 2 || last[len(last)-2].Parts[0].Text != "slow-result" ||
		last[len(last)-1].Parts[0].Text != "fast-result" {
		t.Fatalf("second request messages = %+v, want call order feedback", last)
	}
}

func TestAgentFeedsUnknownToolAsTypedRejection(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{
		{
			Message: gopact.Message{Role: "assistant"},
			Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{{
				ID: "call-1", Name: "missing",
			}}},
		},
		finalResponse("recovered"),
	}}
	target, err := New(testIdentity(), model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("work")},
	}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	requests := model.Requests()
	feedback := requests[1].Messages[len(requests[1].Messages)-1]
	if feedback.Role != "tool" || !strings.Contains(feedback.Parts[0].Text, "unknown tool") {
		t.Fatalf("feedback = %+v, want typed unknown-tool rejection", feedback)
	}
}

func TestAgentConsumesOnlyPendingObservations(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{
		{Message: gopact.Message{Role: "assistant"}, Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{{ID: "call-1", Name: "lookup"}}}},
		{Message: gopact.Message{Role: "assistant"}, Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{{ID: "call-2", Name: "lookup"}}}},
		finalResponse("done"),
	}}
	tool := directTool("lookup", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		return resultOutcome(call, call.ID), nil
	}).(testTool)
	tool.spec.Description = "look up evidence"
	tool.spec.Schema = []byte(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	tool.spec.Metadata = map[string]string{"class": "search"}
	target, err := New(testIdentity(), model,
		WithInstruction("Use tools when evidence is required."),
		WithTools(tool),
	)
	if err != nil {
		t.Fatal(err)
	}
	var prepareOutputs []workflow.NodeEventPayload
	if _, err := target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("work")},
	}, gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
		if event.Type != workflow.EventNodeCompleted || event.NodeID != "prepare" {
			return nil
		}
		var payload workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return err
		}
		prepareOutputs = append(prepareOutputs, payload)
		return nil
	})); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	requests := model.Requests()
	if len(requests) != 3 || len(prepareOutputs) != 3 {
		t.Fatalf("model requests/prepare outputs = %d/%d, want 3/3", len(requests), len(prepareOutputs))
	}
	for index, output := range prepareOutputs {
		if output.Output == nil || output.Output.Type != "gopact.ModelRequest" {
			t.Fatalf("prepare output[%d] = %+v, want gopact.ModelRequest", index, output.Output)
		}
	}
	wantCounts := [][2]int{{0, 0}, {1, 0}, {1, 1}}
	for index, request := range requests {
		got := [2]int{countMessageText(request, "call-1"), countMessageText(request, "call-2")}
		if got != wantCounts[index] {
			t.Fatalf("request[%d] observation counts = %v, want %v", index, got, wantCounts[index])
		}
	}
	wantMessages := []string{"Use tools when evidence is required.", "work", "call-1", "call-2"}
	if got := messageTexts(requests[2]); !reflect.DeepEqual(got, wantMessages) {
		t.Fatalf("final request messages = %v, want %v", got, wantMessages)
	}
	wantTools := []gopact.ToolSpec{tool.spec}
	if !reflect.DeepEqual(requests[2].Tools, wantTools) {
		t.Fatalf("final request tools = %+v, want %+v", requests[2].Tools, wantTools)
	}
}

func countMessageText(request gopact.ModelRequest, text string) int {
	count := 0
	for _, value := range messageTexts(request) {
		if value == text {
			count++
		}
	}
	return count
}

func messageTexts(request gopact.ModelRequest) []string {
	var texts []string
	for _, message := range request.Messages {
		texts = appendMessageTexts(texts, message)
	}
	return texts
}

func appendMessageTexts(texts []string, message gopact.Message) []string {
	for _, part := range message.Parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

func TestAgentHandlesRepairAndRejectsRefusal(t *testing.T) {
	t.Run("repair", func(t *testing.T) {
		model := &scriptedModel{responses: []gopact.ModelResponse{
			{
				Message: gopact.Message{Role: "assistant"},
				Intent: gopact.RepairIntent{Repair: gopact.RepairRequest{
					Reason: "schema", Message: gopact.UserMessage("return JSON"),
				}},
			},
			finalResponse("fixed"),
		}}
		target, err := New(testIdentity(), model)
		if err != nil {
			t.Fatal(err)
		}
		response, err := target.Invoke(context.Background(), agent.Request{Messages: []gopact.Message{gopact.UserMessage("work")}})
		if err != nil || response.Message.Parts[0].Text != "fixed" {
			t.Fatalf("Invoke() = %+v, %v", response, err)
		}
		requests := model.Requests()
		if requests[1].Messages[len(requests[1].Messages)-1].Parts[0].Text != "return JSON" {
			t.Fatalf("repair request = %+v", requests[1])
		}
	})

	t.Run("refusal", func(t *testing.T) {
		target, err := New(testIdentity(), &scriptedModel{responses: []gopact.ModelResponse{{
			Intent: gopact.RefusalIntent{Refusal: gopact.Refusal{Reason: "policy"}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = target.Invoke(context.Background(), agent.Request{Messages: []gopact.Message{gopact.UserMessage("work")}})
		if err == nil || !strings.Contains(err.Error(), "policy") {
			t.Fatalf("Invoke() error = %v, want refusal", err)
		}
	})
}

func TestAgentResumesInterruptedAgentToolWithoutReplayingCompletedWork(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{
		{
			Message: gopact.Message{Role: "assistant"},
			Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{
				{ID: "call-before", Name: "before"},
				{ID: "call-child", Name: "delegate"},
				{ID: "call-after", Name: "after"},
			}},
		},
		finalResponse("done"),
	}}
	var beforeCalls, childRuns, afterCalls int
	before := directTool("before", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		beforeCalls++
		return resultOutcome(call, "before"), nil
	})
	after := directTool("after", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		afterCalls++
		return resultOutcome(call, "after"), nil
	})
	childIdentity := agent.Identity{Name: "delegate-child", Description: "requires approval", Version: "v1"}
	childWorkflow := workflow.New[agent.Request, agent.Response](childIdentity.Name, workflow.WithTopologyVersion(childIdentity.Version))
	work := childWorkflow.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		childRuns++
		return agent.Response{Message: gopact.UserMessage("delegated")}, nil
	})
	work.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{ID: "child-approval"}}, nil
		},
	)))
	childWorkflow.Entry(work)
	childWorkflow.Exit(work)
	child, err := agent.NewWorkflowAgent(childIdentity, childWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	delegate, err := agenttool.New(gopact.ToolSpec{Name: "delegate"}, child, agenttool.AdapterFuncs{
		InputFunc: func(context.Context, gopact.ToolCall) (agent.Request, error) { return agent.Request{}, nil },
		OutputFunc: func(_ context.Context, response agent.Response) (gopact.ToolResult, error) {
			return gopact.ToolResult{Preview: response.Message.Parts[0].Text}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := New(testIdentity(), model, WithTools(before, delegate, after))
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("parent-run"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want Workflow InterruptError", err)
	}
	if beforeCalls != 1 || childRuns != 0 || afterCalls != 0 {
		t.Fatalf("calls before resume = %d/%d/%d, want 1/0/0", beforeCalls, childRuns, afterCalls)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{
		RunID: interrupted.RunID, CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{
			InterruptID: "child-approval", PayloadRef: "resolution://approved",
		}},
	}))
	if err != nil {
		t.Fatalf("resumed Invoke() error = %v", err)
	}
	if response.Message.Parts[0].Text != "done" || beforeCalls != 1 || childRuns != 1 || afterCalls != 1 {
		t.Fatalf("response/calls = %+v/%d/%d/%d, want done and 1/1/1", response, beforeCalls, childRuns, afterCalls)
	}
	if len(model.Requests()) != 2 {
		t.Fatalf("model requests = %d, want no model replay", len(model.Requests()))
	}
}

func TestAgentResumeDoesNotRepeatCommittedWorkflowTurn(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{finalResponse("done")}}
	target, err := New(testIdentity(), model)
	if err != nil {
		t.Fatal(err)
	}
	sinkErr := errors.New("model completion sink failed")
	failed := false
	_, err = target.Invoke(
		context.Background(), agent.Request{}, gopact.WithRunID("react-transition"),
		gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
			if event.RunID == "react-transition" && event.Type == workflow.EventNodeCompleted && event.NodeID == "model" && !failed {
				failed = true
				return sinkErr
			}
			return nil
		}),
	)
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Invoke() error = %v, want sink failure", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{RunID: "react-transition"}))
	if err != nil {
		t.Fatalf("resumed Invoke() error = %v", err)
	}
	if model.next != 1 || response.Message.Parts[0].Text != "done" {
		t.Fatalf("model calls = %d, response = %+v, want committed model output without replay", model.next, response)
	}
}

func TestAgentAppliesInstructionOnceAcrossTurns(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{
		{
			Message: gopact.Message{Role: "assistant"},
			Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{{
				ID: "call-1", Name: "lookup",
			}}},
		},
		finalResponse("done"),
	}}
	tool := directTool("lookup", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		return resultOutcome(call, "result"), nil
	})
	target, err := New(
		testIdentity(),
		model,
		WithInstruction("Use tools when evidence is required."),
		WithTools(tool),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("work")},
	}); err != nil {
		t.Fatal(err)
	}
	for index, request := range model.Requests() {
		instructionCount := countInstructions(request, "Use tools when evidence is required.")
		if instructionCount != 1 {
			t.Fatalf("request[%d] instruction count = %d, want 1", index, instructionCount)
		}
	}
}

func countInstructions(request gopact.ModelRequest, instruction string) int {
	count := 0
	for _, message := range request.Messages {
		if message.Role == "system" && len(message.Parts) == 1 && message.Parts[0].Text == instruction {
			count++
		}
	}
	return count
}

func TestNewRejectsIncompleteIdentity(t *testing.T) {
	identities := []agent.Identity{
		{Description: "tool-using agent", Version: "v1"},
		{Name: "react", Version: "v1"},
		{Name: "react", Description: "tool-using agent"},
	}
	for _, identity := range identities {
		if _, err := New(identity, finalModel("done")); err == nil {
			t.Fatalf("New(%+v) error = nil, want invalid identity", identity)
		}
	}
}

func TestAgentRejectsEmptyToolCallIntent(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{
		{Intent: gopact.ToolCallIntent{}},
		finalResponse("must-not-run"),
	}}
	target, err := New(testIdentity(), model)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("work")},
	})
	if err == nil || !strings.Contains(err.Error(), "no calls") {
		t.Fatalf("Invoke() error = %v, want empty tool intent", err)
	}
	if len(model.Requests()) != 1 {
		t.Fatalf("model requests = %d, want fail without retry", len(model.Requests()))
	}
}

func TestAgentValidatesWholeToolIntentBeforeExecution(t *testing.T) {
	t.Run("duplicate ids across batches", func(t *testing.T) {
		model := &scriptedModel{responses: []gopact.ModelResponse{{
			Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{
				{ID: "duplicate", Name: "direct"},
				{ID: "duplicate", Name: "run"},
			}},
		}}}
		var directCalls, runCalls int
		direct := directTool("direct", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
			directCalls++
			return resultOutcome(call, "direct"), nil
		})
		runTool := &resultRunTool{spec: gopact.ToolSpec{Name: "run"}, calls: &runCalls}
		target, err := New(testIdentity(), model, WithTools(direct, runTool))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Invoke(context.Background(), agent.Request{}); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("Invoke() error = %v, want duplicate call id", err)
		}
		if directCalls != 0 || runCalls != 0 {
			t.Fatalf("tool calls = direct:%d run:%d, want no side effects", directCalls, runCalls)
		}
	})

	t.Run("invalid remaining arguments before interrupt", func(t *testing.T) {
		model := &scriptedModel{responses: []gopact.ModelResponse{{
			Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{
				{ID: "delegate", Name: "delegate"},
				{ID: "invalid", Name: "after", Arguments: []byte(`{"value":`)},
			}},
		}}}
		var delegateCalls int
		delegate := &resultRunTool{spec: gopact.ToolSpec{Name: "delegate"}, calls: &delegateCalls}
		after := directTool("after", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
			return resultOutcome(call, "after"), nil
		})
		target, err := New(testIdentity(), model, WithTools(delegate, after))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Invoke(context.Background(), agent.Request{}); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
			t.Fatalf("Invoke() error = %v, want invalid arguments", err)
		}
		if delegateCalls != 0 {
			t.Fatalf("delegate calls = %d, want validation before execution", delegateCalls)
		}
	})
}

func TestAgentPreservesAllDurableToolReferences(t *testing.T) {
	model := toolThenFinalModel("collect")
	refs := make([]gopact.ArtifactRef, 80)
	for index := range refs {
		refs[index] = gopact.ArtifactRef{URI: fmt.Sprintf("artifact://%d", index)}
	}
	tool := directTool("collect", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		return gopact.ToolResultOutcome{
			CallID: call.ID,
			Name:   call.Name,
			Result: gopact.ToolResult{ArtifactRefs: refs[:70], EffectRefs: refs[70:], Preview: "collected"},
		}, nil
	})
	target, err := New(testIdentity(), model, WithTools(tool))
	if err != nil {
		t.Fatal(err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Artifacts) != len(refs) {
		t.Fatalf("artifacts = %d, want %d", len(response.Artifacts), len(refs))
	}
}

func TestAgentRejectsTypedNilFinalIntent(t *testing.T) {
	model := &scriptedModel{responses: []gopact.ModelResponse{{
		Message: gopact.Message{Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: "must not succeed"}}},
		Intent:  (*gopact.FinalIntent)(nil),
	}}}
	target, err := New(testIdentity(), model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{}); err == nil || !strings.Contains(err.Error(), "nil final") {
		t.Fatalf("Invoke() error = %v, want nil final intent", err)
	}
}

func TestNewRejectsInvalidToolSchema(t *testing.T) {
	tool := directTool("lookup", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
		return resultOutcome(call, "result"), nil
	}).(testTool)
	tool.spec.Schema = []byte(`{"type":`)
	if _, err := New(testIdentity(), finalModel("done"), WithTools(tool)); err == nil {
		t.Fatal("New() error = nil, want invalid tool schema")
	}
}

func TestAgentClassifiesToolFailures(t *testing.T) {
	t.Run("model feedback", func(t *testing.T) {
		model := toolThenFinalModel("lookup")
		tool := directTool("lookup", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
			return gopact.ToolErrorOutcome{
				CallID: call.ID,
				Name:   call.Name,
				Error: gopact.ToolError{
					Kind:              "remote_unavailable",
					Message:           "remote unavailable",
					RetryableForModel: true,
					Feedback:          "retry with another source",
				},
			}, nil
		})
		target, err := New(testIdentity(), model, WithTools(tool))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Invoke(context.Background(), agent.Request{}); err != nil {
			t.Fatal(err)
		}
		requests := model.Requests()
		feedback := requests[1].Messages[len(requests[1].Messages)-1]
		if feedback.Role != "tool" || feedback.Parts[0].Text != "retry with another source" {
			t.Fatalf("feedback = %+v, want retryable model feedback", feedback)
		}
	})

	t.Run("non observable outcome", func(t *testing.T) {
		model := toolThenFinalModel("lookup")
		tool := directTool("lookup", func(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
			return gopact.ToolErrorOutcome{
				CallID: call.ID,
				Name:   call.Name,
				Error:  gopact.ToolError{Kind: "fatal"},
			}, nil
		})
		target, err := New(testIdentity(), model, WithTools(tool))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Invoke(context.Background(), agent.Request{}); !errors.Is(err, agent.ErrToolOutcomeNotObservable) {
			t.Fatalf("Invoke() error = %v, want ErrToolOutcomeNotObservable", err)
		}
	})

	t.Run("executor failure", func(t *testing.T) {
		model := toolThenFinalModel("lookup")
		tool := directTool("lookup", func(context.Context, gopact.ToolCall) (gopact.ToolOutcome, error) {
			return nil, errors.New("transport failed")
		})
		target, err := New(testIdentity(), model, WithTools(tool))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Invoke(context.Background(), agent.Request{}); err == nil || !strings.Contains(err.Error(), "transport failed") {
			t.Fatalf("Invoke() error = %v, want executor failure", err)
		}
		if len(model.Requests()) != 1 {
			t.Fatalf("model requests = %d, want no model retry", len(model.Requests()))
		}
	})
}

type scriptedModel struct {
	mu        sync.Mutex
	responses []gopact.ModelResponse
	requests  []gopact.ModelRequest
	next      int
}

func (model *scriptedModel) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	return gopact.ModelRequest{Messages: append([]gopact.Message(nil), messages...)}
}

func (model *scriptedModel) Invoke(_ context.Context, request gopact.ModelRequest, _ ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.requests = append(model.requests, request)
	if model.next >= len(model.responses) {
		return gopact.ModelResponse{}, errors.New("script exhausted")
	}
	response := model.responses[model.next]
	model.next++
	return response, nil
}

func (model *scriptedModel) Requests() []gopact.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]gopact.ModelRequest(nil), model.requests...)
}

type deterministicFinalModel struct{ text string }

func finalModel(text string) gopact.Model { return deterministicFinalModel{text: text} }

func (model deterministicFinalModel) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	return gopact.ModelRequest{Messages: append([]gopact.Message(nil), messages...)}
}

func (model deterministicFinalModel) Invoke(context.Context, gopact.ModelRequest, ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	return finalResponse(model.text), nil
}

type testTool struct {
	spec    gopact.ToolSpec
	execute func(context.Context, gopact.ToolCall) (gopact.ToolOutcome, error)
}

type resultRunTool struct {
	spec  gopact.ToolSpec
	calls *int
}

func (tool *resultRunTool) Spec() gopact.ToolSpec { return tool.spec }
func (tool *resultRunTool) Invoke(_ context.Context, call gopact.ToolCall, _ ...gopact.RunOption) (gopact.ToolOutcome, error) {
	(*tool.calls)++
	return resultOutcome(call, "run"), nil
}

func directTool(name string, execute func(context.Context, gopact.ToolCall) (gopact.ToolOutcome, error)) agent.Tool {
	return testTool{spec: gopact.ToolSpec{Name: name}, execute: execute}
}

func (tool testTool) Spec() gopact.ToolSpec { return tool.spec }

func (tool testTool) ExecuteTool(ctx context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
	return tool.execute(ctx, call)
}

func resultOutcome(call gopact.ToolCall, preview string) gopact.ToolOutcome {
	return gopact.ToolResultOutcome{
		CallID: call.ID, Name: call.Name, Result: gopact.ToolResult{Preview: preview},
	}
}

func finalResponse(text string) gopact.ModelResponse {
	return gopact.ModelResponse{
		Message: gopact.Message{Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: text}}},
		Intent:  gopact.FinalIntent{},
	}
}

func toolThenFinalModel(name string) *scriptedModel {
	return &scriptedModel{responses: []gopact.ModelResponse{
		{
			Message: gopact.Message{Role: "assistant"},
			Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{{
				ID: "call-1", Name: name,
			}}},
		},
		finalResponse("done"),
	}}
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "react", Description: "tool-using agent", Version: "v1"}
}
