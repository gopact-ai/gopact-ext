# Changelog

<!-- gopact:doc-language: en -->

Chinese documentation: [CHANGELOG_zh.md](CHANGELOG_zh.md)

This changelog records user-visible changes for `gopact-ext`. Each extension is released as an independent Go submodule tag, for example `models/openai/v0.5.19`.

## Unreleased

The current unreleased work improves documentation quality across the repository while preserving the existing mock-only CI and opt-in integration tests for real providers. The 2026-07-02 release line added public-readiness checks, PR governance, parallel CI gates, current module tags, and broader Agnes-backed agent template coverage.

- Update extension modules to `gopact` core `v0.0.39`.
- Publish current extension tags on core `v0.0.39`: `agents/agenttool/v0.1.18`, `agents/planexec/v0.2.19`, `agents/react/v0.2.17`, `agents/supervisor/v0.1.5`, `devagent/filesnapshot/v0.1.16`, `devagent/gitdiff/v0.1.16`, `models/openai/v0.5.19`, `models/ark/v0.2.17`, and `models/agnes/v0.1.20`.
- Add `agents/supervisor`, a provider-neutral template that routes a task to a named child runnable while preserving runtime IDs and event evidence.
- Add mock and Agnes-backed `tests/agents` coverage for supervisor routing to a Plan-Execute child.
- Document ReAct verification export process records and step-export resume as tested extension capabilities.
