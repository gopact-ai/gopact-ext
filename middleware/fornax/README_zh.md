# Fornax middleware

[English documentation](README.md)

`fornax` 包装一个 `gopact` Agent，并把 Agent、Workflow 与节点 span 上报到 Fornax OTLP/HTTP trace endpoint。

配置采用显式注入。middleware 不从环境变量读取 `SpaceID`、`Endpoint` 或 `Authorization`；应用自行决定这些参数的来源和管理方式。

```go
middleware, err := fornax.New(ctx, fornax.Config{
	SpaceID:       spaceID,
	Endpoint:      endpoint,
	Authorization: authorization,
})
if err != nil {
	return err
}
defer middleware.Close(context.Background())

tracedAgent := middleware.Use(target)
response, err := tracedAgent.Invoke(ctx, request)
```

如果 target 的动态类型实现了 `agent.StreamingAgent`，`Use` 会保留 `InvokeStream`；当 target 的静态类型就是 `agent.StreamingAgent` 时，可直接使用 `UseStreaming`。两种入口都会持续追踪到流正常结束、失败或被消费者取消。

`Endpoint` 是完整的 Fornax OTLP/HTTP trace URL。`Authorization` 会原样写入 HTTP `Authorization` header，`SpaceID` 会写入 `cozeloop-workspace-id`。

Agent 调用上报为 `fornax_query`，Workflow RunID 和 SessionID 分别映射为 Fornax `message_id` 和 `thread_id`；嵌套 Workflow run 上报为 `agent`；名为 `model` 和 `tool` 的节点分别使用对应的 Fornax span type，其他节点使用 `graph`。传给 `Invoke` 的已有 event sink 会继续生效。应用退出时调用 `Close`，以刷新尚未上报的 span。

## ID 对应关系

| 来源 | 上报值 | 在 Fornax 中的含义 |
| --- | --- | --- |
| `Config.SpaceID` | HTTP header `cozeloop-workspace-id` | 目标工作空间，不是 trace ID 或 span ID。 |
| 根 Workflow `RunID` | `messaging.message.id` 和 `gopact.run_id` | 分别作为 Fornax `message_id` 和 gopact run 标识。 |
| 嵌套 Workflow `RunID` | `gopact.run_id` | 子 Agent run 标识，不会替换根 `message_id`。 |
| Workflow `SessionID` | 根 span 上的 `session.id` | Fornax `thread_id`，用于归组相关消息。 |
| Workflow `ParentRunID` | OTel 父子关系和 `gopact.parent_run_id` | 将嵌套 Agent span 关联到父 run。 |
| Workflow `DefinitionID` | `agent_name`；嵌套 Agent span 名称 | 标识 Workflow/Agent 定义。 |
| 节点 `NodeID` | 节点 span 名称和 `gopact.node_id` | `model`、`tool` 使用对应 span type，其他值使用 `graph`。 |
| 节点 `ActivationID` / `AttemptID` | `gopact.activation_id` / `gopact.attempt_id` | 分别标识一次节点激活及其中一次执行尝试。 |
| OTel Trace ID / Span ID | OTLP 原生 ID | 从输入 context 继承或由 OTel 生成，不从 RunID、SessionID 派生。 |

Trace input/output 遵循 Fornax 的 4 MB 字段限制；超限值不再附加到 span，并在 `cut_off` 中标记。流式 chunk 仍会完整转发给应用，不受该上报限制影响。

核心 Workflow event 契约只包含生命周期元数据，不包含 provider 请求体、token 用量或模型/工具结果。因此，该 middleware 可以上报节点耗时和状态，但不会虚构这些 provider 专属字段。
