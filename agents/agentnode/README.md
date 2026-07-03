# agentnode

[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/agentnode.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/agentnode)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`agentnode` adapts an A2A agent into a typed `graph.NodeFunc`. Use it when a workflow graph should delegate one step to a domain agent while preserving A2A child events in the parent graph event stream.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/agentnode@v0.1.7` and run `(cd agents/agentnode && go test -count=1 ./...)` before changing behavior.

```go
node, err := agentnode.New(
	childAgent,
	func(ctx context.Context, state State) (a2a.Task, error) {
		ids, _ := gopact.RuntimeIDsFromContext(ctx)
		return a2a.Task{ID: "plan-task", IDs: ids, Input: state.Input}, nil
	},
	func(ctx context.Context, state State, result a2a.Result) (State, error) {
		state.Plan = result.Output
		return state, nil
	},
)
```

The adapter prefers `a2a.StreamingAgent` when available. A2A message, artifact, status, completion, and failure events are emitted through `graph.EmitNodeEvent`, so the parent graph stream keeps child evidence with `graph_parent_node` and `graph_parent_step` metadata.
