# 🧩 gopact-ext

<!-- gopact:doc-language: zh -->

[English documentation](README.md)

`gopact` 新设计下的官方扩展仓库。

> **仅支持 Go 1.27+。** 本项目围绕泛型方法构建，也借此庆祝我们眼中 Go 近十年来最具影响力的语言演进之一。Go 1.27 正式发布前，本项目需要开发版工具链，应视为预览而非稳定版本。

## 扩展目录

### 模型 Adapter

| 包 | 适用场景 |
| --- | --- |
| [`models/openai`](./models/openai) | OpenAI-compatible 对话与流式 API |
| [`models/agnes`](./models/agnes) | 通过 OpenAI-compatible API 使用 Agnes |
| [`models/glm`](./models/glm) | 通过 OpenAI-compatible API 使用 GLM/Zhipu |
| [`models/fake`](./models/fake) | 确定性的离线测试与示例 |

### Agent 组合

| 包 | 适用场景 |
| --- | --- |
| [`agents/agenttool`](./agents/agenttool) | 把 child Agent 暴露为 typed tool |
| [`agents/react`](./agents/react) | 运行 model-tool-model 推理循环 |
| [`agents/sequential`](./agents/sequential) | 让任务按顺序流经多个 child Agent |
| [`agents/parallel`](./agents/parallel) | 并行分发独立任务并汇总结果 |
| [`agents/loop`](./agents/loop) | 重复执行一个 Agent，直到满足停止条件 |
| [`agents/router`](./agents/router) | 为每个请求选择一个 child Agent |
| [`agents/planexec`](./agents/planexec) | 计划、执行、重规划并生成报告 |
| [`agents/supervisor`](./agents/supervisor) | 协调委派给 child Agent 的工作 |
| [`agents/deep`](./agents/deep) | 执行显式的长任务计划 |
| [`agents/deepresearch`](./agents/deepresearch) | 发现、验证并汇总带引用的证据 |

### Store

| 包 | 适用场景 |
| --- | --- |
| [`stores/sqlite`](./stores/sqlite) | 本地持久化 checkpoint、history、control 与 runlog |

完整的可运行应用见 [gopact-examples](https://github.com/gopact-ai/gopact-examples)。

所有官方 Agent 都由一个 Workflow 表达算法状态机。checkpoint、interrupt/resume、child lineage、节点事实和控制历史只由 Workflow runtime 所有；Agent 层保留模型、工具、计划、路由和研究等领域能力。

## Agent 持久化执行

基于 Workflow 的 Agent 构造器都提供 `WithWorkflowOptions`，因此生产环境可以直接为官方 Agent 配置持久化与租约策略：

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

持久化恢复要求用相同的 Agent name、version 和拓扑重新构造 Agent，打开同一个 Store，并恢复同一个 RunID；不要传入冲突的 SessionID。外部副作用仍是 at-least-once 语义，必须使用稳定的幂等键。

## Breaking 迁移

本次重建使用各模块的下一个 pre-v1 minor 统一发布，不复用旧 patch 版本。主要入口变化：

| 旧入口 | 新入口 |
|---|---|
| `react.New(ChatModel, *tools.Registry, ...)` / `NewModelAgent` | `react.New(agent.Identity, gopact.Model, ...Option)`；tool 通过 `WithTools(...agent.Tool)` 注入 |
| `agenttool.New(a2a.Agent, ...Option)` | `agenttool.New(gopact.ToolSpec, agent.Agent, agenttool.Adapter)`；child 作为 typed Workflow invokable 执行 |
| 旧 graph/template 版 `planexec`、`supervisor` | 传入 immutable `agent.Directory` 与各自 Planner/Replanner/Decider；Workflow 保存状态与执行事实 |
