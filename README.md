# 🧩 gopact-ext

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

Official extensions for the redesigned `gopact` core.

> **Go 1.27+ only.** This project is built around generic methods and celebrates what we see as one of Go's most consequential language changes of the past decade. Until Go 1.27 is officially released, it requires a development toolchain and should be treated as a preview, not a stable release.

## Extension catalog

### Model adapters

| Package | Use it for |
| --- | --- |
| [`models/openai`](./models/openai) | OpenAI-compatible chat and streaming APIs |
| [`models/agnes`](./models/agnes) | Agnes through its OpenAI-compatible API |
| [`models/glm`](./models/glm) | GLM/Zhipu through its OpenAI-compatible API |
| [`models/fake`](./models/fake) | Deterministic offline tests and examples |

### Agent compositions

| Package | Use it for |
| --- | --- |
| [`agents/agenttool`](./agents/agenttool) | Expose a child Agent as a typed tool |
| [`agents/react`](./agents/react) | Run a model-tool-model reasoning loop |
| [`agents/sequential`](./agents/sequential) | Pass work through ordered child Agents |
| [`agents/parallel`](./agents/parallel) | Fan out independent work and reduce the results |
| [`agents/loop`](./agents/loop) | Repeat one Agent until a stop condition |
| [`agents/router`](./agents/router) | Select one child Agent for each request |
| [`agents/planexec`](./agents/planexec) | Plan, execute, replan, and report |
| [`agents/supervisor`](./agents/supervisor) | Coordinate delegated child-Agent work |
| [`agents/deep`](./agents/deep) | Execute explicit long-horizon task plans |
| [`agents/deepresearch`](./agents/deepresearch) | Discover, verify, and synthesize cited evidence |

### Stores

| Package | Use it for |
| --- | --- |
| [`stores/sqlite`](./stores/sqlite) | Durable local checkpoints, history, control, and run logs |

For complete runnable applications, see [gopact-examples](https://github.com/gopact-ai/gopact-examples).

Every official Agent expresses its algorithmic state machine as one Workflow. Workflow exclusively owns checkpoint, interrupt/resume, child lineage, node facts, and control history; the Agent layer retains model, tool, planning, routing, and research behavior.

## Durable Agent execution

Workflow-backed Agent constructors expose `WithWorkflowOptions`, so production persistence and lease policy can be configured without bypassing the official Agent:

```go
store, err := sqlite.Open("agent.db")
if err != nil {
	return err
}
defer store.Close()

target, err := react.New(identity, model, react.WithWorkflowOptions(
	workflow.WithCheckpointer(store),
	workflow.WithJournal(store),
	workflow.WithCheckpointLease(3*time.Minute, time.Minute),
))
if err != nil {
	return err
}

response, err := target.Invoke(ctx, request, gopact.WithRunID("run-123"))
```

Durable resume requires reconstructing the same Agent topology with the same identity name and version, opening the same Store, and resuming the same RunID. Do not supply a conflicting SessionID. External side effects remain at-least-once and must use a stable idempotency key.

## Breaking migration

This rebuild will ship all affected modules at their next pre-v1 minor rather than reusing an old patch line.

| Previous entry point | Replacement |
|---|---|
| `react.New(ChatModel, *tools.Registry, ...)` / `NewModelAgent` | `react.New(agent.Identity, gopact.Model, ...Option)` with tools supplied by `WithTools(...agent.Tool)` |
| `agenttool.New(a2a.Agent, ...Option)` | `agenttool.New(gopact.ToolSpec, agent.Agent, agenttool.Adapter)`; the child executes as a typed Workflow invokable |
| graph/template-based `planexec` and `supervisor` | immutable `agent.Directory` plus package Planner/Replanner/Decider contracts; Workflow stores state and execution facts |
