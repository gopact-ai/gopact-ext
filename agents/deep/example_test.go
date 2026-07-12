package deep_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/deep"
	"github.com/gopact-ai/gopact/agent"
)

type exampleChild struct{}

func (exampleChild) Identity() agent.Identity {
	return agent.Identity{Name: "writer", Description: "writes reports", Version: "v1"}
}

func (exampleChild) Invoke(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error) {
	return agent.Response{Message: gopact.UserMessage("report ready")}, nil
}

// ExampleNew demonstrates planning one task and delegating it to a catalog Agent.
func ExampleNew() {
	catalog := agent.NewCatalog()
	if err := catalog.Add(exampleChild{}); err != nil {
		fmt.Println("add:", err)
		return
	}
	directory, err := catalog.Compile()
	if err != nil {
		fmt.Println("compile:", err)
		return
	}
	target, err := deep.New(
		agent.Identity{Name: "deep", Description: "runs a task plan", Version: "v1"},
		directory,
		deep.PlannerFunc(func(context.Context, deep.PlanInput) ([]deep.Task, error) {
			return []deep.Task{{ID: "write", Description: "write the report", AgentName: "writer"}}, nil
		}),
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
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("prepare a report")}},
		gopact.WithSessionID("deep-example"),
		gopact.WithRunID("deep-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: report ready
}
