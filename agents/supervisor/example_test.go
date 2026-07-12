package supervisor_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/supervisor"
	"github.com/gopact-ai/gopact/agent"
)

type localAgent struct {
	identity agent.Identity
	result   string
}

func (target localAgent) Identity() agent.Identity { return target.identity }

func (target localAgent) Invoke(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error) {
	return agent.Response{Message: gopact.UserMessage(target.result)}, nil
}

func ExampleNew() {
	catalog := agent.NewCatalog()
	if err := catalog.Add(localAgent{
		identity: agent.Identity{Name: "research", Description: "collects release evidence", Version: "v1"},
		result:   "evidence collected",
	}); err != nil {
		fmt.Println("add research:", err)
		return
	}
	directory, err := catalog.Compile()
	if err != nil {
		fmt.Println("compile:", err)
		return
	}
	target, err := supervisor.New(
		agent.Identity{Name: "coordinator", Description: "coordinates release research", Version: "v1"},
		directory,
		supervisor.DeciderFunc(func(_ context.Context, input supervisor.DecisionInput) (supervisor.Decision, error) {
			if len(input.Results) == 0 {
				return supervisor.Decision{
					Kind:  supervisor.DecisionDelegate,
					Child: "research",
					Request: agent.Request{
						Messages: []gopact.Message{gopact.UserMessage("collect evidence")},
					},
				}, nil
			}
			response := input.Results[0].Response
			return supervisor.Decision{Kind: supervisor.DecisionFinal, Response: &response}, nil
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
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("research release")}},
		gopact.WithSessionID("supervisor-example"),
		gopact.WithRunID("supervisor-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: evidence collected
}
