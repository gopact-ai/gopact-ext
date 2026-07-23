package workflowtest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
				`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","name":"slow","arguments":"{\"q\":\"first\"}","call_id":"call_1"}}`,
				`{"type":"response.output_item.done","item":{"id":"fc_2","type":"function_call","status":"completed","name":"fast","arguments":"{\"q\":\"second\"}","call_id":"call_2"}}`,
				`{"type":"response.completed","response":{"id":"resp-tool"}}`,
			)
			return
		}
		outputs := make(map[string]string)
		for _, item := range request.Input {
			if item.Type == "function_call_output" {
				outputs[item.CallID] = item.Output
			}
		}
		if len(outputs) != 2 || outputs["call_1"] != "slow-result" || outputs["call_2"] != "fast-result" {
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
	fastFinished := make(chan struct{})
	target, err := react.New(
		agent.Identity{Name: "codex-react-test", Description: "tests Codex tools", Version: "v1"},
		model,
		react.WithTools(
			codexTool{name: "slow", output: "slow-result", wait: fastFinished},
			codexTool{name: "fast", output: "fast-result"},
		),
	)
	if err != nil {
		t.Fatalf("react.New() error = %v", err)
	}
	sink := &toolCompletionSink{fastFinished: fastFinished}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response, err := target.Invoke(
		ctx,
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("look it up")}},
		gopact.WithEventSink(sink),
	)
	if err != nil {
		t.Fatalf("Agent.Invoke() error = %v", err)
	}
	if text := messageText(response.Message); text != "The answer is 42." {
		t.Fatalf("agent response = %q", text)
	}
	if calls.Load() != 2 {
		t.Fatalf("model calls = %d, want 2", calls.Load())
	}
	completed := sink.completedCalls()
	if len(completed) != 2 || completed[0] != "call_2" || completed[1] != "call_1" {
		t.Fatalf("tool completion order = %v, want fast before slow", completed)
	}
}

type codexTool struct {
	name   string
	output string
	wait   <-chan struct{}
}

func (tool codexTool) Spec() gopact.ToolSpec {
	return gopact.ToolSpec{
		Name:        tool.name,
		Description: "returns the answer",
		Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}
}

func (tool codexTool) ExecuteTool(ctx context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
	if tool.wait != nil {
		select {
		case <-tool.wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return gopact.ToolResultOutcome{
		CallID: call.ID,
		Name:   call.Name,
		Result: gopact.ToolResult{Preview: tool.output},
	}, nil
}

type toolCompletionSink struct {
	mu           sync.Mutex
	completed    []string
	fastFinished chan struct{}
	fastOnce     sync.Once
}

func (*toolCompletionSink) Emit(context.Context, gopact.Event) error { return nil }

func (sink *toolCompletionSink) EmitToolEvent(_ context.Context, event gopact.ToolEvent) error {
	if event.Type != gopact.ToolEventCallFinished {
		return nil
	}
	sink.mu.Lock()
	sink.completed = append(sink.completed, event.Call.ID)
	sink.mu.Unlock()
	if event.Call.ID == "call_2" {
		sink.fastOnce.Do(func() { close(sink.fastFinished) })
	}
	return nil
}

func (sink *toolCompletionSink) completedCalls() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]string(nil), sink.completed...)
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
