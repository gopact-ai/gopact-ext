# gopact-ext

<!-- gopact:doc-language: en -->

Official extensions for the redesigned `gopact` core.

> **Go 1.27+ only.** This project is built around generic methods and celebrates what we see as one of Go's most consequential language changes of the past decade. Until Go 1.27 is officially released, it requires a development toolchain and should be treated as a preview, not a stable release.

This repository provides OpenAI-compatible providers, Workflow-native Agents, and SQLite persistence:

- `models/fake`: deterministic model adapter for tests and examples.
- `models/openai`: reusable OpenAI-compatible HTTP model adapter.
- `models/agnes`: Agnes OpenAI-compatible model adapter.
- `models/glm`: GLM/Zhipu OpenAI-compatible model adapter.
- `agents/react`: the model-intent and tool-feedback loop.
- `agents/sequential`, `agents/parallel`, and `agents/loop`: deterministic composition agents.
- `agents/agenttool`: Workflow child Agent-to-tool adapter.
- `agents/router`, `agents/planexec`, and `agents/supervisor`: routing, plan-execute-replan, and multi-agent supervision.
- `agents/deep` and `agents/deepresearch`: long-horizon and research-specific agents.
- `stores/sqlite`: SQLite implementation of Workflow checkpoint, history, control, and runlog contracts.

Every official Agent expresses its algorithmic state machine as one Workflow. Workflow exclusively owns checkpoint, interrupt/resume, child lineage, node facts, and control history; the Agent layer retains model, tool, planning, routing, and research behavior.

## Breaking migration

This rebuild will ship all affected modules at their next pre-v1 minor rather than reusing an old patch line.

| Previous entry point | Replacement |
|---|---|
| `react.New(ChatModel, *tools.Registry, ...)` / `NewModelAgent` | `react.New(agent.Identity, gopact.Model, ...Option)` with tools supplied by `WithTools(...agent.Tool)` |
| `agenttool.New(a2a.Agent, ...Option)` | `agenttool.New(gopact.ToolSpec, agent.Agent, agenttool.Adapter)`; the child executes as a typed Workflow invokable |
| graph/template-based `planexec` and `supervisor` | immutable `agent.Directory` plus package Planner/Replanner/Decider contracts; Workflow stores state and execution facts |
