# 🧩 gopact-ext

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

Official extensions for the redesigned `gopact` core.

> **Go 1.27+ only.** This project is built around generic methods and celebrates what we see as one of Go's most consequential language changes of the past decade. Until Go 1.27 is officially released, it requires a development toolchain and should be treated as a preview, not a stable release.

Until the coordinated RC modules are published, this repository is a source-development
checkout: clone `gopact` beside `gopact-ext`; the committed `go.work` joins the source
modules without changing their published dependency contract. A standalone clone is
supported only after the corresponding tagged modules have passed clean-consumer verification.

## Release verification

The release order is `gopact` → `gopact-ext` → `gopact-ext/stores` → `gopact-examples`. Before tags exist, the three repositories are tested from coordinated source checkouts through Go workspaces. Increase the prefix after each approved tag; the final default remains strict over all four:

```bash
./scripts/clean-consumer.sh --prefix-count 1 scripts/release-versions.txt
./scripts/clean-consumer.sh --prefix-count 2 scripts/release-versions.txt
./scripts/clean-consumer.sh --prefix-count 3 scripts/release-versions.txt
./scripts/clean-consumer.sh scripts/release-versions.txt
```

The script starts from an empty consumer, checks exact selected versions, and rejects missing modules, consumer or tagged-module `replace` directives, pseudo-versions, and `v0.0.0`. `--validate-only` checks manifest structure without downloading tags. During staged publication, only a successful prefix is release evidence. RCs remain production-evaluation candidates until Go 1.27 stable gates and burn-in pass.

## Extension catalog

### Model adapters

| Package | Use it for |
| --- | --- |
| [`models/openai`](./models/openai) | OpenAI-compatible chat and streaming APIs |
| [`models/agnes`](./models/agnes) | Agnes through its OpenAI-compatible API |
| [`models/glm`](./models/glm) | GLM/Zhipu through its OpenAI-compatible API |
| [`models/fake`](./models/fake) | Deterministic offline tests and examples |

### Authentication

| Package | Use it for |
| --- | --- |
| [`models/openai/codexauth`](./models/openai/codexauth) | OpenAI Codex device-code login and OAuth token refresh |

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
| [`stores/dbstore`](./stores/dbstore) | Shared GORM checkpoint, lease, fencing, RunLog, and retention logic |
| [`stores/sqlite`](./stores/sqlite) | Pure-Go local persistence using SQLite rollback journal mode |
| [`stores/mysql`](./stores/mysql) | Multi-host persistence on MySQL |
| [`stores/mariadb`](./stores/mariadb) | Multi-host persistence on MariaDB through the MySQL dialect |
| [`stores/postgres`](./stores/postgres) | Multi-host persistence on PostgreSQL |

For complete runnable applications, see [gopact-examples](https://github.com/gopact-ai/gopact-examples).

Every official Agent expresses its algorithmic state machine as one Workflow. Workflow exclusively owns checkpoint, interrupt/resume, child lineage, node facts, and control history; the Agent layer retains model, tool, planning, routing, and research behavior.

## Durable Agent execution

Workflow-backed Agent constructors expose `WithWorkflowOptions`, so production persistence and lease policy can be configured without bypassing the official Agent:

```go
if err := sqlite.Migrate("agent.db"); err != nil { // deployment migration stage
	return err
}
store, err := sqlite.Open("agent.db")
if err != nil {
	return err
}
defer store.Close()

target, err := react.New(identity, model, react.WithWorkflowOptions(
	workflow.WithStore(store),
	workflow.WithCheckpointLease(3*time.Minute, time.Minute),
))
if err != nil {
	return err
}

response, err := target.Invoke(ctx, request, gopact.WithRunID("run-123"))
```

Durable resume requires reconstructing the same Agent topology with the same identity name and version, opening the same Store, and resuming the same RunID. Do not supply a conflicting SessionID. External side effects remain at-least-once and must use a key stable across recovery, such as `RunInfo.RunID + "/" + RunInfo.ActivationID`.

That key is reliable only when the external API natively deduplicates it, or when application code writes a uniquely constrained dedup/outbox record in the same database transaction as its business data. `gopact` cannot atomically combine a checkpoint transaction with an arbitrary remote API and does not provide a generic outbox. An explicit business retry intended to produce another side effect must use a new operation key.

Use `workflow.MemoryStore` only for tests and short-lived processes. The SQLite Store is for one machine or multiple processes that safely share the same local database file. File databases require `journal_mode=DELETE`; explicit non-DELETE DSNs are rejected, and the first conversion of a persistent WAL database needs a maintenance window with all other SQLite connections stopped. SQLite, MySQL, MariaDB, and PostgreSQL all use `Migrate(dsn)` in the deployment stage followed by `Open(dsn)` in application instances; a true in-memory SQLite database is initialized by `Open`. Multi-host deployments should use the MySQL, MariaDB, or PostgreSQL Store. These stores derive lease expiry from the database clock inside the ownership transaction. For an existing schema, stop and drain all old writers before migration. The database advisory lock serializes migrators but does not make mixed-version writers safe. Schedule terminal and standalone-journal purge; exceptionally long active Runs can compact only their confirmed contiguous prefix with explicit `AllowHistoryLoss`, because the removed Retry/Fork/audit history cannot be reconstructed.

## Breaking migration

This rebuild will ship all affected modules at their next pre-v1 minor rather than reusing an old patch line.

| Previous entry point | Replacement |
|---|---|
| `react.New(ChatModel, *tools.Registry, ...)` / `NewModelAgent` | `react.New(agent.Identity, gopact.Model, ...Option)` with tools supplied by `WithTools(...agent.Tool)` |
| `agenttool.New(a2a.Agent, ...Option)` | `agenttool.New(gopact.ToolSpec, agent.Agent, agenttool.Adapter)`; the child executes as a typed Workflow invokable |
| graph/template-based `planexec` and `supervisor` | immutable `agent.Directory` plus package Planner/Replanner/Decider contracts; Workflow stores state and execution facts |
