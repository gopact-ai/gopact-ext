package sequential_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/sequential"
	"github.com/gopact-ai/gopact/agent"
)

type exampleAgent struct {
	name string
}

func (target exampleAgent) Identity() agent.Identity {
	return agent.Identity{Name: target.name, Description: target.name + " release notes", Version: "v1"}
}

func (target exampleAgent) Invoke(_ context.Context, request agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	if target.name == "writer" {
		return agent.Response{Message: gopact.UserMessage("draft")}, nil
	}
	text := request.Messages[0].Parts[0].Text
	return agent.Response{Message: gopact.UserMessage("approved: " + text)}, nil
}

func ExampleNew() {
	catalog := agent.NewCatalog()
	if err := catalog.Add(exampleAgent{name: "writer"}); err != nil {
		fmt.Println("add writer:", err)
		return
	}
	if err := catalog.Add(exampleAgent{name: "reviewer"}); err != nil {
		fmt.Println("add reviewer:", err)
		return
	}
	directory, err := catalog.Compile()
	if err != nil {
		fmt.Println("compile:", err)
		return
	}
	target, err := sequential.New(
		agent.Identity{Name: "publishing", Description: "writes and reviews release notes", Version: "v1"},
		directory,
		[]string{"writer", "reviewer"},
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
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("release notes")}},
		gopact.WithSessionID("sequential-example"),
		gopact.WithRunID("sequential-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: approved: draft
}
