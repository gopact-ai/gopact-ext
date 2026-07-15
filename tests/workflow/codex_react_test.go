package workflowtest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact-ext/models/openai/codex"
	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
	"github.com/gopact-ai/gopact/agent"
)

func TestCodexModelRunsReactToolRoundTrip(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Output string `json:"output"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if calls.Add(1) == 1 {
			writeCodexSSE(t, w,
				`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","name":"lookup","arguments":"{\"q\":\"answer\"}","call_id":"call_1"}}`,
				`{"type":"response.completed","response":{"id":"resp-tool"}}`,
			)
			return
		}
		if len(request.Input) != 3 || request.Input[2].Type != "function_call_output" ||
			request.Input[2].CallID != "call_1" || request.Input[2].Output != "42" {
			t.Errorf("tool continuation input = %+v", request.Input)
		}
		writeCodexSSE(t, w,
			`{"type":"response.output_text.delta","delta":"The answer is 42."}`,
			`{"type":"response.completed","response":{"id":"resp-final"}}`,
		)
	}))
	defer server.Close()

	model, err := codex.New(
		"gpt-test",
		codex.StaticTokenSource(codexauth.Tokens{AccessToken: "access"}),
		codex.WithBaseURL(server.URL),
		codex.WithInsecureHTTP(),
		codex.WithMaxAttempts(1),
	)
	if err != nil {
		t.Fatalf("codex.New() error = %v", err)
	}
	target, err := react.New(
		agent.Identity{Name: "codex-react-test", Description: "tests Codex tools", Version: "v1"},
		model,
		react.WithTools(codexLookupTool{}),
	)
	if err != nil {
		t.Fatalf("react.New() error = %v", err)
	}
	response, err := target.Invoke(t.Context(), agent.Request{Messages: []gopact.Message{gopact.UserMessage("look it up")}})
	if err != nil {
		t.Fatalf("Agent.Invoke() error = %v", err)
	}
	if text := messageText(response.Message); text != "The answer is 42." {
		t.Fatalf("agent response = %q", text)
	}
	if calls.Load() != 2 {
		t.Fatalf("model calls = %d, want 2", calls.Load())
	}
}

type codexLookupTool struct{}

func (codexLookupTool) Spec() gopact.ToolSpec {
	return gopact.ToolSpec{
		Name:        "lookup",
		Description: "returns the answer",
		Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}
}

func (codexLookupTool) ExecuteTool(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
	return gopact.ToolResultOutcome{
		CallID: call.ID,
		Name:   call.Name,
		Result: gopact.ToolResult{Preview: "42"},
	}, nil
}

func writeCodexSSE(t *testing.T, w http.ResponseWriter, events ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		if _, err := io.WriteString(w, "data: "+event+"\n\n"); err != nil {
			t.Errorf("write SSE: %v", err)
			return
		}
	}
}

func messageText(message gopact.Message) string {
	var text strings.Builder
	for _, part := range message.Parts {
		if part.Type == gopact.MessagePartTypeText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}
