# gopact-ext

<!-- gopact:doc-language: zh -->

`gopact` 新设计下的官方扩展仓库。

> **仅支持 Go 1.27+。** 本项目围绕泛型方法构建，也借此庆祝我们眼中 Go 近十年来最具影响力的语言演进之一。Go 1.27 正式发布前，本项目需要开发版工具链，应视为预览而非稳定版本。

当前提供 OpenAI-compatible provider、Workflow-native Agent 和 SQLite 持久化 adapter：

- `models/fake`：用于测试和示例的确定性 model adapter；
- `models/openai`：可复用的 OpenAI-compatible HTTP model adapter；
- `models/agnes`：Agnes OpenAI-compatible model adapter；
- `models/glm`：GLM/Zhipu OpenAI-compatible model adapter；
- `agents/react`：通用 model intent/tool feedback loop；
- `agents/sequential`、`agents/parallel`、`agents/loop`：确定性组合 Agent；
- `agents/agenttool`：Workflow child Agent-to-tool adapter；
- `agents/router`、`agents/planexec`、`agents/supervisor`：路由、计划执行重规划和多 Agent 监督；
- `agents/deep`、`agents/deepresearch`：长任务和研究领域 Agent；
- `stores/sqlite`：Workflow checkpoint、history、control 与 runlog 的 SQLite 实现。

所有官方 Agent 都由一个 Workflow 表达算法状态机。checkpoint、interrupt/resume、child lineage、节点事实和控制历史只由 Workflow runtime 所有；Agent 层保留模型、工具、计划、路由和研究等领域能力。

## Breaking 迁移

本次重建使用各模块的下一个 pre-v1 minor 统一发布，不复用旧 patch 版本。主要入口变化：

| 旧入口 | 新入口 |
|---|---|
| `react.New(ChatModel, *tools.Registry, ...)` / `NewModelAgent` | `react.New(agent.Identity, gopact.Model, ...Option)`；tool 通过 `WithTools(...agent.Tool)` 注入 |
| `agenttool.New(a2a.Agent, ...Option)` | `agenttool.New(gopact.ToolSpec, agent.Agent, agenttool.Adapter)`；child 作为 typed Workflow invokable 执行 |
| 旧 graph/template 版 `planexec`、`supervisor` | 传入 immutable `agent.Directory` 与各自 Planner/Replanner/Decider；Workflow 保存状态与执行事实 |
