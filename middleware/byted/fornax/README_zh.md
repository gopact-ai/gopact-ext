# ByteDance Fornax middleware

[English documentation](README.md)

`fornax` 是 ByteDance 专属中间件，用于包装 `gopact` Agent，并把 Agent、Workflow 与节点 span 上报到 Fornax OTLP/HTTP trace endpoint。

配置采用显式注入。middleware 不从环境变量读取凭据；应用自行决定这些参数的来源和管理方式。

```go
middleware, err := fornax.New(ctx, fornax.Config{
	AK:     ak,
	SK:     sk,
	Region: "CN", // 可选；按需使用 SG、US、Asia-SouthEastBD 或 I18N-DEV
})
if err != nil {
	return err
}
defer middleware.Close(context.Background())

tracedAgent := middleware.Use(target)
response, err := tracedAgent.Invoke(ctx, request)
```

如果 target 的动态类型实现了 `agent.StreamingAgent`，`Use` 会保留 `InvokeStream`；当 target 的静态类型就是 `agent.StreamingAgent` 时，可直接使用 `UseStreaming`。两种入口都会持续追踪到流正常结束、失败或被消费者取消。

`AK` 和 `SK` 是 Fornax 空间凭据。`Region` 可选，会显式用于 Fornax 鉴权和 trace endpoint 选择，不依赖 `FORNAX_CUSTOM_REGION`。`SpaceID` 可选；传入时必须与 AK/SK 鉴权得到的空间一致。`Endpoint` 是高级覆盖项，用于指定完整 OTLP/HTTP trace URL。

Agent 调用上报为 `fornax_query`，Workflow RunID 和 SessionID 分别映射为 Fornax `message_id` 和 `thread_id`；嵌套 Workflow run 上报为 `agent`；名为 `model` 和 `tool` 的节点分别使用对应的 Fornax span type，其他节点使用 `graph`。传给 `Invoke` 的已有 event sink 会继续生效。应用退出时调用 `Close`，以刷新尚未上报的 span。

## ID 对应关系

| 来源 | 上报值 | 在 Fornax 中的含义 |
| --- | --- | --- |
| AK/SK 鉴权得到的 workspace | HTTP header `cozeloop-workspace-id` | 目标工作空间，不是 trace ID 或 span ID。 |
| 根 Workflow `RunID` | `messaging.message.id` 和 `gopact.run_id` | 分别作为 Fornax `message_id` 和 gopact run 标识。 |
| 嵌套 Workflow `RunID` | `gopact.run_id` | 子 Agent run 标识，不会替换根 `message_id`。 |
| Workflow `SessionID` | 根 span 上的 `session.id` | Fornax `thread_id`，用于归组相关消息。 |
| Workflow `ParentRunID` | OTel 父子关系和 `gopact.parent_run_id` | 将嵌套 Agent span 关联到父 run。 |
| Workflow `DefinitionID` | `agent_name`；嵌套 Agent span 名称 | 标识 Workflow/Agent 定义。 |
| 节点 `NodeID` | 节点 span 名称和 `gopact.node_id` | `model`、`tool` 使用对应 span type，其他值使用 `graph`。 |
| 节点 `ActivationID` / `AttemptID` | `gopact.activation_id` / `gopact.attempt_id` | 分别标识一次节点激活及其中一次执行尝试。 |
| `ToolCall.ID` | typed tool span 上的 `tool_call_id` | 标识模型请求的工具调用，不是 OTel Span ID。 |
| OTel Trace ID / Span ID | OTLP 原生 ID | 从输入 context 继承或由 OTel 生成，不从 RunID、SessionID 派生。 |

Trace input/output 遵循 Fornax 的 4 MB 字段限制；超限值不再附加到 span，并在 `cut_off` 中标记。流式 chunk 仍会完整转发给应用，不受该上报限制影响。

核心 Workflow event 契约只包含生命周期元数据，不包含 provider 请求体、token 用量或模型/工具结果。支持 component observation 的 Agent 可以额外发出实时 typed model/tool observation；该 middleware 会用这些 observation 富化同一个节点 span，填入真实的请求、响应、token 用量、模型名、tool call ID 和工具结果。模型或工具 adapter 没有发出的字段不会被虚构。
