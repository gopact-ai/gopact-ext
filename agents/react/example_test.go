package react_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact/agent"
)

type exampleModel struct{}

func (*exampleModel) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	return gopact.ModelRequest{Messages: messages}
}

func (*exampleModel) Invoke(_ context.Context, request gopact.ModelRequest, _ ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	for _, message := range request.Messages {
		if message.Role == "tool" {
			return gopact.ModelResponse{
				Message: gopact.Message{
					Role:  "assistant",
					Parts: []gopact.MessagePart{{Type: "text", Text: "answer from evidence"}},
				},
				Intent: gopact.FinalIntent{},
			}, nil
		}
	}
	return gopact.ModelResponse{
		Message: gopact.Message{Role: "assistant"},
		Intent: gopact.ToolCallIntent{Calls: []gopact.ToolCall{{
			ID: "lookup-1", Name: "lookup",
		}}},
	}, nil
}

type exampleLookupTool struct{ calls atomic.Int64 }

func (*exampleLookupTool) Spec() gopact.ToolSpec {
	return gopact.ToolSpec{Name: "lookup", Description: "looks up evidence"}
}

func (tool *exampleLookupTool) ExecuteTool(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
	tool.calls.Add(1)
	return gopact.ToolResultOutcome{
		CallID: call.ID,
		Name:   call.Name,
		Result: gopact.ToolResult{Preview: "evidence"},
	}, nil
}

// TestExampleAgentCanBeReusedConcurrently verifies that one configured ReAct Agent can safely serve independent runs.
func TestExampleAgentCanBeReusedConcurrently(t *testing.T) {
	tool := &exampleLookupTool{}
	target, err := react.New(
		agent.Identity{Name: "assistant", Description: "answers questions", Version: "v1"},
		&exampleModel{},
		react.WithTools(tool),
	)
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	for run := 1; run <= 2; run++ {
		go func(run int) {
			response, err := target.Invoke(
				context.Background(),
				agent.Request{Messages: []gopact.Message{gopact.UserMessage("find evidence")}},
				gopact.WithSessionID(fmt.Sprintf("react-example-%d", run)),
				gopact.WithRunID(fmt.Sprintf("react-run-%d", run)),
			)
			if err != nil {
				results <- fmt.Errorf("run %d: %w", run, err)
				return
			}
			if len(response.Message.Parts) == 0 {
				results <- fmt.Errorf("run %d response has no parts", run)
				return
			}
			if got := response.Message.Parts[0].Text; got != "answer from evidence" {
				results <- fmt.Errorf("run %d response = %q", run, got)
				return
			}
			results <- nil
		}(run)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if calls := tool.calls.Load(); calls != 2 {
		t.Fatalf("tool calls = %d, want 2", calls)
	}
}

// ExampleNew demonstrates a ReAct model-tool-model round trip with workflow event logging.
func ExampleNew() {
	model := &exampleModel{}
	tool := &exampleLookupTool{}
	target, err := react.New(
		agent.Identity{Name: "assistant", Description: "answers questions", Version: "v1"},
		model,
		react.WithTools(tool),
	)
	if err != nil {
		fmt.Println("new:", err)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logEvent := gopact.WithEventHandler(func(ctx context.Context, event gopact.Event) error {
		logger.InfoContext(
			ctx,
			"workflow event",
			"type", event.Type,
			"session_id", event.SessionID,
			"run_id", event.RunID,
			"parent_run_id", event.ParentRunID,
			"node_id", event.NodeID,
			"node_version", event.NodeExecutionVersion,
			"origin", event.Origin,
		)
		return nil
	})
	response, err := target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("find evidence")}},
		gopact.WithSessionID("react-example"),
		gopact.WithRunID("react-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	if calls := tool.calls.Load(); calls != 1 {
		fmt.Printf("tool calls: %d\n", calls)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: answer from evidence
}
