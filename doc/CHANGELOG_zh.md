# Changelog

<!-- gopact:doc-language: zh -->

[英文文档](./CHANGELOG.md)

## 中文

本文件记录 `gopact-ext` 对用户可见的变更。每个 extension 作为独立 Go submodule 发布，tag 使用模块路径前缀，例如 `models/openai/v0.5.23`。

## Unreleased

- 将 extension modules 同步到 `gopact` core `v0.0.45`。
- 发布基于 core `v0.0.45` 的当前 extension tag：`agents/agentnode/v0.1.3`、`agents/agenttool/v0.1.22`、`agents/planexec/v0.2.23`、`agents/react/v0.2.21`、`agents/supervisor/v0.1.9`、`devagent/filesnapshot/v0.1.20`、`devagent/gitdiff/v0.1.20`、`devagent/workspace/v0.1.1`、`models/openai/v0.5.23`、`models/ark/v0.2.21`、`models/agnes/v0.1.24`。
- 增加 `agents/agentnode`：把 A2A agent 适配为 graph node，并把子 A2A events 保留在父 graph stream 中。
- 增加 `agents/supervisor`：provider-neutral template，可将任务路由到指定子 runnable，并保留 runtime IDs 与 event evidence。
- 增加 `agents/humanreview`：provider-neutral 的人工审批 graph gate，支持 step-export 和 checkpoint resume。
- 增加 `agents/scheduler`：provider-neutral 的后台 worker 原语，支持队列状态转换、lease renewal、retry、stop、dead-letter、drain 和 schedule evidence。
- 增加 `devagent/selfbootstrap`：provider-neutral 的 Dev Agent workflow，编排 analyze、plan、write、test、review 证据并产出 run export 与 verification report。
- 增加 `devagent/workspace`：本地仓库适配器，应用调用方提供的 patch，并采集 self-bootstrap 所需的 diff、file snapshot、command 和 CI gate evidence。
- 增加 `tests/agents` 的 mock 与 Agnes-backed 覆盖，固化 supervisor 路由到 Plan-Execute 子 agent 的组合路径。
- 增加 `tests/agents` 的 Agnes-backed 覆盖，固化 A2A AgentNode graph 委托路径。
- 增加 `tests/agents` 的 mock 覆盖，固化 HumanReview gate 在 graph workflow 中的组合路径。
- 将 ReAct verification export process records 与 step-export resume 纳入已测试 extension 能力契约。
- 重写根 README、模块 README 和 `doc/` 文档，按公开开源项目标准补齐定位、安装、验证、真实 provider 测试、安全和治理说明。
- 保持 CI mock-only，并继续通过 integration build tag 支持 OpenAI、Ark、Agnes 和跨 agent template 的真实服务测试。

## 2026-07-02

- 增加 public readiness 检查，扫描 tracked file 和 commit message 中的高置信敏感信息模式。
- 增加 PR governance workflows：admin-authored PR 在必需门禁通过后自动 squash merge；non-admin-authored PR 需要至少一名 admin 审批。
- 将 CI 拆成 hygiene、unit、race、static、coverage、security 等独立 job，并保留聚合的 `ci/test` required check。
- 发布当前 extension tag：`agents/agenttool/v0.1.14`、`agents/planexec/v0.2.15`、`agents/react/v0.2.13`、`devagent/filesnapshot/v0.1.12`、`devagent/gitdiff/v0.1.12`、`models/openai/v0.5.15`、`models/ark/v0.2.13`、`models/agnes/v0.1.16`。
- `models/agnes` 基于 `models/openai/v0.5.15`，覆盖 streaming、tool calling、structured output、thinking toggle、错误分类、取消和超时。
- `tests/agents` 覆盖 ReAct、Plan-Execute、Agent-as-Tool 和 Agnes-backed template 组合路径。

## 2026-07-01

- 发布第一批可组合 extension：ReAct、Plan-Execute、agent-as-tool、file snapshot、git diff、OpenAI-compatible provider、Ark provider、Agnes provider。
- OpenAI provider 支持 Chat Completions 和 Responses，包含 SSE streaming、tool calling、structured output、thinking/reasoning 参数、Responses image/text input 和 reasoning summary 映射。
- Ark provider 通过 Volcengine Ark SDK 接入，支持 API key 与 AK/SK。
- Dev-agent helper 提供文件快照和 git diff 证据采集，不修改工作区。
