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

Trace input/output 遵循 Fornax 的 4 MB 字段限制；超限值不再附加到 span，并在 `cut_off` 中标记。流式 chunk 仍会完整转发给应用，不受该上报限制影响。

核心 Workflow event 契约只包含生命周期元数据，不包含 provider 请求体、token 用量或模型/工具结果。因此，该 middleware 可以上报节点耗时和状态，但不会虚构这些 provider 专属字段。
