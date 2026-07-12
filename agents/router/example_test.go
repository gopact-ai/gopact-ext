package router_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/router"
	"github.com/gopact-ai/gopact/agent"
)

type exampleAgent struct {
	name   string
	result string
}

func (target exampleAgent) Identity() agent.Identity {
	return agent.Identity{Name: target.name, Description: target.name + " support", Version: "v1"}
}

func (target exampleAgent) Invoke(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error) {
	return agent.Response{Message: gopact.UserMessage(target.result)}, nil
}

// ExampleNew demonstrates selecting one child Agent from request content.
func ExampleNew() {
	catalog := agent.NewCatalog()
	for _, child := range []exampleAgent{
		{name: "billing", result: "billing support"},
		{name: "technical", result: "technical support"},
	} {
		if err := catalog.Add(child); err != nil {
			fmt.Println("add child:", err)
			return
		}
	}
	directory, err := catalog.Compile()
	if err != nil {
		fmt.Println("compile:", err)
		return
	}
	target, err := router.New(
		agent.Identity{Name: "support-router", Description: "routes support requests", Version: "v1"},
		directory,
		router.SelectorFunc(func(_ context.Context, request agent.Request, _ []agent.Identity) (router.Selection, error) {
			text := request.Messages[0].Parts[0].Text
			if strings.Contains(strings.ToLower(text), "timeout") {
				return router.Selection{Child: "technical"}, nil
			}
			return router.Selection{Child: "billing"}, nil
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
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("API timeout")}},
		gopact.WithSessionID("router-example"),
		gopact.WithRunID("router-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: technical support
}
