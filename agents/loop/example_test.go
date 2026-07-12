package loop_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/loop"
	"github.com/gopact-ai/gopact/agent"
)

type exampleAgent struct{}

func (exampleAgent) Identity() agent.Identity {
	return agent.Identity{Name: "worker", Description: "improves a draft", Version: "v1"}
}

func (exampleAgent) Invoke(_ context.Context, request agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	text := request.Messages[0].Parts[0].Text
	return agent.Response{Message: gopact.UserMessage(text + "!")}, nil
}

// ExampleNew demonstrates repeating one Agent until a stop condition is reached.
func ExampleNew() {
	target, err := loop.New(
		agent.Identity{Name: "iteration", Description: "improves a draft three times", Version: "v1"},
		exampleAgent{},
		loop.ConditionFunc(func(_ context.Context, iteration loop.Iteration) (loop.Decision, error) {
			if iteration.Number < 3 {
				return loop.DecisionContinue, nil
			}
			return loop.DecisionStop, nil
		}),
		loop.WithMaxIterations(3),
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
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("draft")}},
		gopact.WithSessionID("loop-example"),
		gopact.WithRunID("loop-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: draft!!!
}
