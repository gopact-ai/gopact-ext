package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
	"github.com/gopact-ai/gopact/agent"
)

func TestModelRunsReactToolRoundTrip(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if calls.Add(1) == 1 {
			writeSSE(t, w,
				`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","name":"lookup","arguments":"{\"q\":\"answer\"}","call_id":"call_1"}}`,
				`{"type":"response.completed","response":{"id":"resp-tool"}}`,
			)
			return
		}
		if len(request.Input) != 3 || request.Input[2].Type != "function_call_output" || request.Input[2].CallID != "call_1" || request.Input[2].Output != "42" {
			t.Errorf("tool continuation input = %+v", request.Input)
		}
		writeSSE(t, w,
			`{"type":"response.output_text.delta","delta":"The answer is 42."}`,
			`{"type":"response.completed","response":{"id":"resp-final"}}`,
		)
	}))
	defer server.Close()

	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))
	target, err := react.New(
		agent.Identity{Name: "codex-react-test", Description: "tests Codex tools", Version: "v1"},
		model,
		react.WithTools(lookupTool{}),
	)
	if err != nil {
		t.Fatalf("react.New() error = %v", err)
	}
	response, err := target.Invoke(t.Context(), agent.Request{Messages: []gopact.Message{gopact.UserMessage("look it up")}})
	if err != nil {
		t.Fatalf("Agent.Invoke() error = %v", err)
	}
	if text := messageText(t, response.Message); text != "The answer is 42." {
		t.Fatalf("agent response = %q", text)
	}
	if calls.Load() != 2 {
		t.Fatalf("model calls = %d, want 2", calls.Load())
	}
}

type lookupTool struct{}

func (lookupTool) Spec() gopact.ToolSpec {
	return gopact.ToolSpec{
		Name:        "lookup",
		Description: "returns the answer",
		Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}
}

func (lookupTool) ExecuteTool(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
	return gopact.ToolResultOutcome{
		CallID: call.ID,
		Name:   call.Name,
		Result: gopact.ToolResult{Preview: "42"},
	}, nil
}
