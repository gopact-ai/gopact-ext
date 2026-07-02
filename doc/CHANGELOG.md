# Changelog

<!-- gopact:doc-language: en -->

Chinese documentation: [CHANGELOG_zh.md](CHANGELOG_zh.md)

This changelog records user-visible changes for `gopact-ext`. Each extension is released as an independent Go submodule tag, for example `models/openai/v0.5.18`.

## Unreleased

The current unreleased work improves documentation quality across the repository while preserving the existing mock-only CI and opt-in integration tests for real providers. The 2026-07-02 release line added public-readiness checks, PR governance, parallel CI gates, current module tags, and broader Agnes-backed agent template coverage.

- Update extension modules to `gopact` core `v0.0.38`.
- Publish current extension tags on core `v0.0.38`: `agents/agenttool/v0.1.17`, `agents/planexec/v0.2.18`, `agents/react/v0.2.16`, `agents/supervisor/v0.1.4`, `devagent/filesnapshot/v0.1.15`, `devagent/gitdiff/v0.1.15`, `models/openai/v0.5.18`, `models/ark/v0.2.16`, and `models/agnes/v0.1.19`.
- Add `agents/supervisor`, a provider-neutral template that routes a task to a named child runnable while preserving runtime IDs and event evidence.
- Add mock and Agnes-backed `tests/agents` coverage for supervisor routing to a Plan-Execute child.
- Document ReAct verification export process records and step-export resume as tested extension capabilities.
