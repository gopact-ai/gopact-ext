# 🧩 gopact-ext

<!-- gopact:doc-language: zh -->

[English documentation](README.md)

`gopact` 新设计下的官方扩展仓库。

> **仅支持 Go 1.27+。** 本项目围绕泛型方法构建，也借此庆祝我们眼中 Go 近十年来最具影响力的语言演进之一。

仓库提交的 `go.work` 会联结本仓库内的全部模块。跨仓 CI 还会用当前
`gopact` 与示例源码验证兼容性，但不会改变任何正式发布的依赖契约。

扩展按领域拆分为独立发版的模块，一个模块可以包含多个包。只添加实际所需包所属的模块：

```bash
go get github.com/gopact-ai/gopact-ext/agents/react@v0.4.0
go get github.com/gopact-ai/gopact-ext/models/agnes@v0.2.0
go get github.com/gopact-ai/gopact-ext/stores@v0.2.0
```

根模块不再充当一次安装全部扩展的聚合包。各领域模块的当前版本见
[发布清单](./scripts/release-versions.txt)。

## 发布验证

发布顺序由清单定义；每行声明模块、精确正式版本，以及要在干净消费者中
编译的真实包。验证脚本使用扩展 Agent 命名空间下的受控模块路径，因此可以
编译仓库的 `internal` 发布锚点，同时保持这些包对外不可导入。只有下一个精确
版本已经能从指定代理获取，才可以增加检查前缀；省略前缀时检查完整清单：

```bash
./scripts/clean-consumer.sh --validate-only scripts/release-versions.txt
./scripts/clean-consumer.sh --prefix-count N scripts/release-versions.txt
./scripts/clean-consumer.sh scripts/release-versions.txt
```

脚本从空 consumer 开始，校验实际选择的精确版本，并拒绝缺失或重复模块、越出所属 module 的检查包、consumer 或 tagged module 中的 `replace`、pseudo-version 和 `v0.0.0`。`--validate-only` 只检查 manifest 结构，不下载 tag。分阶段发布时，只有成功通过的 prefix 才能作为发布证据。

## 扩展目录

### 模型 Adapter

| 包 | 适用场景 |
| --- | --- |
| [`models/openai`](./models/openai) | OpenAI 对话、Responses、embedding、审核、媒体、文件与分片上传 API |
| [`models/openai/codex`](./models/openai/codex) | ChatGPT plan 的 Codex 调用、账号模型发现与订阅用量 |
| [`models/agnes`](./models/agnes) | Agnes 对话、模型发现、图像生成/编辑与异步视频 |
| [`models/glm`](./models/glm) | GLM Coding Plan 对话与用量，以及通用 embedding、媒体、工具、文件与 Agent API |
| [`models/fake`](./models/fake) | 确定性的离线测试与示例 |

各 provider 的能力以其公开上游契约为准，不虚构一个最低公共能力集：

| Provider | 生成与 runtime API | 模型发现 | 用量与配额 |
| --- | --- | --- | --- |
| OpenAI API key | Chat/Completions/Responses、embedding、审核、图像、语音、视频、文件与分片上传 | 列出和读取模型 | 使用独立 `AdminClient` 与 Admin API key 查询组织用量和成本 |
| ChatGPT Codex OAuth | Responses SSE 模型调用 | 当前 ChatGPT 账号可用模型 | ChatGPT plan 窗口、credits、消费控制与附加限制 |
| GLM/Z.AI API key | Coding Plan 对话；异步对话、embedding、审核、图像/视频、语音/转写、工具、文件/文档解析、OCR 与专用 Agent | 列出和读取通用 API 模型 | Coding Plan 配额，以及模型和工具用量 |
| Agnes API key | 对话、图像生成/编辑与异步视频 | API 模型列表 | 不提供：Agnes 没有文档化的公开 API-key 订阅用量 endpoint |
| Fake | 确定性对话与 embedding | 一个确定性模型 | 不适用 |

OpenAI 组织用量属于 API 平台计量，不等同于 ChatGPT/Codex 订阅用量。Agnes 没有公开的 embedding 契约，因此不实现 `gopact.Embedder`。

### 认证

| 包 | 适用场景 |
| --- | --- |
| [`models/openai/codexauth`](./models/openai/codexauth) | OpenAI Codex 设备码登录与 OAuth token 刷新 |

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

原有的 `agents/deep` 和 `agents/deepresearch` 包已经移除。它们是带有强主张的
端到端组合，没有可直接替换的包。仍在导入这些包的应用可暂时停留在根模块
`v0.6.0` 并逐步迁移。升级前应完成或排空由这些包创建的持久化运行；新版本
无法重建或恢复这些 checkpoint。

### Store

| 包 | 适用场景 |
| --- | --- |
| [`stores/dbstore`](./stores/dbstore) | 共享的 GORM checkpoint、租约、fencing、RunLog 与 retention 逻辑 |
| [`stores/sqlite`](./stores/sqlite) | 使用 SQLite rollback journal 的纯 Go 本地持久化 |
| [`stores/mysql`](./stores/mysql) | 基于 MySQL 的多主机持久化 |
| [`stores/mariadb`](./stores/mariadb) | 通过 MySQL 方言支持 MariaDB 多主机持久化 |
| [`stores/postgres`](./stores/postgres) | 基于 PostgreSQL 的多主机持久化 |

### Middleware

| 包 | 适用场景 |
| --- | --- |
| [`middleware/byted/fornax`](./middleware/byted/fornax) | ByteDance 专属中间件，通过显式配置向 Fornax 上报 Agent、Workflow 与节点 trace |

完整的可运行应用见 [gopact-examples](https://github.com/gopact-ai/gopact-examples)。

所有官方 Agent 都由一个 Workflow 表达算法状态机。checkpoint、interrupt/resume、child lineage、节点事实和控制历史只由 Workflow runtime 所有；Agent 层保留模型、工具、计划、路由和研究等领域能力。

## Agent 持久化执行

基于 Workflow 的 Agent 构造器都提供 `WithWorkflowOptions`，因此生产环境可以直接为官方 Agent 配置持久化与租约策略：

```go
if err := sqlite.Migrate("agent.db"); err != nil { // 部署迁移阶段
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

持久化恢复要求用相同的 Agent name、version 和拓扑重新构造 Agent，打开同一个 Store，并恢复同一个 RunID；不要传入冲突的 SessionID。外部副作用仍是 at-least-once 语义，必须使用跨恢复稳定的 key，例如 `RunInfo.RunID + "/" + RunInfo.ActivationID`。

该 key 只有在两种模式下才可靠：外部 API 原生按 key 去重；或者业务在修改业务数据的同一数据库事务中，写入带唯一约束的 dedup/outbox 记录。`gopact` 无法把 checkpoint 事务与任意远程 API 合并成一个原子事务，也不提供通用 outbox。如果显式业务重试要再次产生副作用，必须使用新的 operation key。

`workflow.MemoryStore` 只适合测试和短生命周期进程。SQLite Store 适用于单机，或安全共享同一个本地数据库文件的多进程；文件库强制使用 `journal_mode=DELETE`，显式指定其他 journal mode 的 DSN 会被拒绝。旧 WAL 数据库首次转换时必须安排维护窗口，先停止其他 SQLite 连接。SQLite、MySQL、MariaDB、PostgreSQL 都统一为部署阶段执行 `Migrate(dsn)`、应用实例调用 `Open(dsn)`；真正的内存 SQLite 由 `Open` 初始化。多主机部署应使用 MySQL、MariaDB 或 PostgreSQL Store；这些 Store 会在 ownership transaction 内用数据库时钟生成并校验租约到期时间。已有 schema 升级前必须停止并排空全部旧 writer。数据库 advisory lock 只会串行化迁移器，不能让新旧 writer 安全混跑。服务必须调度终态 Run 与独立 journal 清理；极长的 active Run 只能在显式 `AllowHistoryLoss` 后压缩连续的已确认前缀，因为被删的 Retry/Fork/审计历史无法恢复。

## 不兼容迁移

本次重建为受影响模块发布各自 1.0 前的下一个次版本，不复用旧的修订版本。主要入口变化：

| 旧入口 | 新入口 |
|---|---|
| 把根模块 `github.com/gopact-ai/gopact-ext` 当作扩展合集 | 添加实际使用的 Agent、模型、Store 或中间件包所属的领域模块 |
| `react.New(ChatModel, *tools.Registry, ...)` / `NewModelAgent` | `react.New(agent.Identity, gopact.Model, ...Option)`；工具通过 `WithTools(...agent.Tool)` 注入 |
| `agenttool.New(a2a.Agent, ...Option)` | `agenttool.New(gopact.ToolSpec, agent.Agent, agenttool.Adapter)`；子 Agent 作为带类型的 Workflow 调用对象执行 |
| 旧图/模板版 `planexec`、`supervisor` | 传入不可变的 `agent.Directory` 与各自的 Planner/Replanner/Decider；Workflow 保存状态与执行事实 |
| `planexec.Planner.Plan(context.Context, planexec.PlanInput)` | `planexec.Planner.Plan(context.Context, agent.Request)`；`Replanner` 接收当前计划和已完成步骤的结果 |
