# Changelog

<!-- gopact:doc-language: en -->

Chinese documentation: [CHANGELOG_zh.md](CHANGELOG_zh.md)

This changelog records user-visible changes for `gopact-ext`. Each extension is released as an independent Go submodule tag, for example `models/openai/v0.5.23`.

## Unreleased

The current unreleased work adds graph-node A2A composition while preserving the existing mock-only CI and opt-in integration tests for real providers. The 2026-07-02 release line added public-readiness checks, PR governance, parallel CI gates, current module tags, and broader Agnes-backed agent template coverage.

- Update extension modules to `gopact` core `v0.0.49`.
- Keep the current extension tag set documented: `agents/agentnode/v0.1.4`, `agents/agenttool/v0.1.23`, `agents/humanreview/v0.1.1`, `agents/planexec/v0.2.24`, `agents/react/v0.2.22`, `agents/scheduler/v0.1.1`, `agents/supervisor/v0.1.10`, `devagent/filesnapshot/v0.1.21`, `devagent/gitdiff/v0.1.21`, `devagent/selfbootstrap/v0.1.2`, `devagent/workspace/v0.1.3`, `models/openai/v0.5.24`, `models/ark/v0.2.22`, and `models/agnes/v0.1.25`.
- Add `agents/agentnode`, an A2A-to-graph adapter that preserves child A2A events in the parent graph stream.
- Add `agents/supervisor`, a provider-neutral template that routes a task to a named child runnable while preserving runtime IDs and event evidence.
- Add `agents/humanreview`, a provider-neutral human approval gate for graph workflows with step-export and checkpoint resume support.
- Add `agents/scheduler`, a provider-neutral background worker primitive with queue transitions, lease renewal, retry, stop, dead-letter, drain, and schedule evidence.
- Add `devagent/selfbootstrap`, a provider-neutral Dev Agent workflow that coordinates analyze, plan patch proposal policy, write, test, and review evidence into a run export and verification report.
- Add `devagent/workspace`, a local repository adapter that applies policy-approved plan patches or caller-provided patches and captures self-bootstrap diff, file snapshot, command, and CI gate evidence.
- Add mock and Agnes-backed `tests/agents` coverage for supervisor routing to a Plan-Execute child.
- Add Agnes-backed `tests/agents` coverage for A2A AgentNode graph delegation.
- Add mock `tests/agents` coverage for HumanReview gate composition inside graph workflows.
- Document ReAct verification export process records and step-export resume as tested extension capabilities.
