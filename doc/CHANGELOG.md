# Changelog

<!-- gopact:doc-language: en -->

Chinese documentation: [CHANGELOG_zh.md](CHANGELOG_zh.md)

This changelog records user-visible changes for `gopact-ext`. Each extension is released as an independent Go submodule tag, for example `models/openai/v0.5.23`.

## Unreleased

The current unreleased work adds graph-node A2A composition while preserving the existing mock-only CI and opt-in integration tests for real providers. The 2026-07-02 release line added public-readiness checks, PR governance, parallel CI gates, current module tags, and broader Agnes-backed agent template coverage.

- Update extension modules to `gopact` core `v0.0.44`.
- Publish current extension tags on core `v0.0.44`: `agents/agentnode/v0.1.3`, `agents/agenttool/v0.1.22`, `agents/planexec/v0.2.23`, `agents/react/v0.2.21`, `agents/supervisor/v0.1.9`, `devagent/filesnapshot/v0.1.20`, `devagent/gitdiff/v0.1.20`, `models/openai/v0.5.23`, `models/ark/v0.2.21`, and `models/agnes/v0.1.24`.
- Add `agents/agentnode`, an A2A-to-graph adapter that preserves child A2A events in the parent graph stream.
- Add `agents/supervisor`, a provider-neutral template that routes a task to a named child runnable while preserving runtime IDs and event evidence.
- Add mock and Agnes-backed `tests/agents` coverage for supervisor routing to a Plan-Execute child.
- Add Agnes-backed `tests/agents` coverage for A2A AgentNode graph delegation.
- Document ReAct verification export process records and step-export resume as tested extension capabilities.
