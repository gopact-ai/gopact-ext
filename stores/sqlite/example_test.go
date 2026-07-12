package sqlite_test

import (
	"context"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/stores/sqlite"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

func ExampleStore() {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			panic(err)
		}
	}()

	identity := agent.Identity{Name: "echo", Description: "echoes one message", Version: "v1"}
	wf := workflow.New[agent.Request, agent.Response](
		identity.Name,
		workflow.WithTopologyVersion(identity.Version),
		workflow.WithStrictCheckpointer(store),
		workflow.WithStrictJournal(store),
	)
	echo := wf.Node("echo", func(_ context.Context, request agent.Request) (agent.Response, error) {
		return agent.Response{Message: request.Messages[0]}, nil
	})
	wf.Entry(echo)
	wf.Exit(echo)
	target, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		panic(err)
	}
	_, err = target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("hello")}},
		gopact.WithSessionID("session-1"),
	)
	if err != nil {
		panic(err)
	}
	runs, err := workflow.ListSessionRuns(
		context.Background(),
		store,
		workflow.SessionRunsRequest{SessionID: "session-1"},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(runs))
	// Output: 1
}
