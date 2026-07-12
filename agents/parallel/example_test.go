package parallel_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/parallel"
	"github.com/gopact-ai/gopact/agent"
)

type exampleAgent struct {
	name   string
	result string
}

func (target exampleAgent) Identity() agent.Identity {
	return agent.Identity{Name: target.name, Description: target.name + " review", Version: "v1"}
}

func (target exampleAgent) Invoke(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error) {
	return agent.Response{Message: gopact.UserMessage(target.result)}, nil
}

func ExampleNew() {
	catalog := agent.NewCatalog()
	for _, child := range []exampleAgent{
		{name: "security", result: "security: pass"},
		{name: "quality", result: "quality: pass"},
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
	target, err := parallel.New(
		agent.Identity{Name: "review-panel", Description: "runs independent reviews", Version: "v1"},
		directory,
		[]string{"security", "quality"},
		parallel.ReducerFunc(func(_ context.Context, results []parallel.BranchResult) (agent.Response, error) {
			texts := make([]string, len(results))
			for index, result := range results {
				texts[index] = result.Response.Message.Parts[0].Text
			}
			return agent.Response{Message: gopact.UserMessage(strings.Join(texts, "; "))}, nil
		}),
		parallel.WithMaxParallelism(2),
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
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("review release")}},
		gopact.WithSessionID("parallel-example"),
		gopact.WithRunID("parallel-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: security: pass; quality: pass
}
