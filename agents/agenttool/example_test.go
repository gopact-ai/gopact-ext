package agenttool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/agenttool"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

func ExampleNew() {
	identity := agent.Identity{Name: "delegate", Description: "completes delegated work", Version: "v1"}
	wf := workflow.New[agent.Request, agent.Response](identity.Name)
	work := wf.Node("work", func(_ context.Context, request agent.Request) (agent.Response, error) {
		text := request.Messages[0].Parts[0].Text
		return agent.Response{Message: gopact.UserMessage("completed: " + text)}, nil
	})
	wf.Entry(work)
	wf.Exit(work)
	child, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		fmt.Println("workflow agent:", err)
		return
	}

	tool, err := agenttool.New(
		gopact.ToolSpec{Name: "delegate"},
		child,
		agenttool.AdapterFuncs{
			InputFunc: func(_ context.Context, call gopact.ToolCall) (agent.Request, error) {
				var arguments struct {
					Task string `json:"task"`
				}
				if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
					return agent.Request{}, err
				}
				return agent.Request{Messages: []gopact.Message{gopact.UserMessage(arguments.Task)}}, nil
			},
			OutputFunc: func(_ context.Context, response agent.Response) (gopact.ToolResult, error) {
				return gopact.ToolResult{Preview: response.Message.Parts[0].Text}, nil
			},
		},
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
	outcome, err := tool.Invoke(
		context.Background(),
		gopact.ToolCall{
			ID:        "call-1",
			Name:      "delegate",
			Arguments: json.RawMessage(`{"task":"review the release"}`),
		},
		gopact.WithSessionID("agenttool-example"),
		gopact.WithRunID("agenttool-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	result, ok := outcome.(gopact.ToolResultOutcome)
	if !ok {
		fmt.Println("outcome: unexpected type")
		return
	}
	fmt.Println(result.Result.Preview)
	// Output: completed: review the release
}
