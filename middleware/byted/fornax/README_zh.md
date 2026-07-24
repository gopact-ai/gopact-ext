# ByteDance Fornax middleware

[English documentation](README.md)

`fornax` 是 ByteDance 专属中间件，用于包装 `gopact` Agent，并把 Agent、Workflow 与节点 span 上报到 Fornax trace ingest endpoint。

配置采用显式注入。middleware 不从环境变量读取凭据；应用自行决定这些参数的来源和管理方式。

## 最小用法

```go
middleware, err := fornax.New(ctx, fornax.Config{
	AK: ak,
	SK: sk,
})
if err != nil {
	return err
}
defer middleware.Close(context.Background())

tracedAgent := middleware.Use(target)
response, err := tracedAgent.Invoke(ctx, request)
```

默认只上报运行元数据，不上报 Agent 消息、model 请求与响应、tool arguments、结果 preview、流式输出或详细错误信息。

## 完整配置

```go
middleware, err := fornax.New(ctx, fornax.Config{
	AK:      ak,
	SK:      sk,
	SpaceID: "12345", // 可选；校验 AK/SK 鉴权得到的 workspace
	Region: "CN", // 可选；按需使用 SG、US、Asia-SouthEastBD 或 I18N-DEV
	Endpoint: "https://fornax.bytedance.net/open-api/observability/traces/ingest", // 可选覆盖
	PSM:      "your.service.psm", // 可选；默认 unknown_psm
	UserID:   "default-user",
	DeviceID: "default-device",
	CaptureContent: false, // 安全默认值；开启前先阅读“内容采集”
	Metadata: map[string]string{
		"tenant": "tenant-1",
	},
})
if err != nil {
	return err
}
defer middleware.Close(context.Background())

tracedAgent := middleware.Use(target)
response, err := tracedAgent.Invoke(ctx, request)
```

## 内容采集

只有应用明确批准把请求与响应内容导出到 Fornax 时，才设置 `CaptureContent: true`。开启后，exporter 可能填充 root 和 Agent span 的顶层 `input`、`output` 字段；收到相应组件事件时，也会填充 model 或 tool span。内容可能包括消息、工具定义与参数、结果预览，以及聚合后的流式输出。原始错误会写入 `tags_string["error"]`。该配置的零值为 `false`，不支持按单次请求覆盖。

应用主动放入 `Config.Metadata`、`agent.Request.Metadata` 或 `WithMetadata` 的值始终会作为 tag 上报，不受 `CaptureContent` 控制。

关闭内容采集时仍保留运行元数据：span 层级、run/session/node 标识、model/tool 名称、tool call ID、token 用量、finish reason、错误状态、耗时和 application 显式提供的 tags。原始 error 仍会返回给 application。

## 单次请求标签

`Config` 中的 `UserID`、`DeviceID` 和 `Metadata` 是默认值。`agent.Request.Metadata` 也会作为本次调用的字符串 tag 上报。身份或元数据随请求变化时，使用 context helper：

```go
ctx = fornax.WithUserID(ctx, "user-456")
ctx = fornax.WithDeviceID(ctx, "device-456")
ctx = fornax.WithMetadata(ctx, map[string]string{"request_id": "req-1"})
response, err := tracedAgent.Invoke(ctx, request)
```

如果 target 的动态类型实现了 `agent.StreamingAgent`，`Use` 会保留 `InvokeStream`；当 target 的静态类型就是 `agent.StreamingAgent` 时，可直接使用 `UseStreaming`。两种入口都会持续追踪到流正常结束、失败或被消费者取消。

`AK` 和 `SK` 是 Fornax 空间凭据。`Region` 可选，会显式用于 Fornax 鉴权和 trace endpoint 选择，不依赖 `FORNAX_CUSTOM_REGION`。`SpaceID` 可选；传入时必须与 AK/SK 鉴权得到的空间一致。`Endpoint` 是高级覆盖项，用于指定完整 Fornax trace ingest URL。`PSM` 会写入 Fornax 鉴权 body，并作为 span `service_name` 和 tag `psm` 上报；未传时默认 `unknown_psm`，与 Fornax SDK 的兜底行为一致。`UserID`、`DeviceID` 和 `Metadata` 会作为字符串 tag 附加到所有上报 span，也可以通过 `WithUserID`、`WithDeviceID` 和 `WithMetadata` 按请求覆盖。

Agent 调用上报为 `fornax_query`，其下包含一个 `Agent` span。Workflow RunID 和 SessionID 分别映射为 Fornax `message_id` 和 `thread_id`；嵌套 Workflow run 上报为 `Agent`；名为 `model` 和 `tool` 的节点分别使用对应的 Fornax span type，其他节点使用 `graph`。传给 `Invoke` 的已有 event sink 会继续生效。应用退出时调用 `Close`，以刷新尚未上报的 span。

## ID 对应关系

| 来源 | 上报值 | 在 Fornax 中的含义 |
| --- | --- | --- |
| AK/SK 鉴权得到的 workspace | trace ingest `workspace_id` | 目标工作空间，不是 trace ID 或 span ID。 |
| 通过 `RunOptions` 传入的调用 `RunID` | root query span 上的 `tags_string["message_id"]` | Fornax message ID。 |
| Workflow 生命周期事件中的 `RunID` | `tags_string["gopact.run_id"]`；根 Workflow 还会写入 `tags_string["message_id"]` | 标识根或子 Workflow run；子 run 不会替换根 message ID。 |
| Workflow `SessionID` | `tags_string["thread_id"]` | 用于归组相关消息的上报 span。 |
| `Config.PSM` | 鉴权 `psm`、span `service_name` 和 span tag `psm` | 上报服务身份；默认 `unknown_psm`。 |
| `Config.UserID` / `Config.DeviceID`，或 context `WithUserID` / `WithDeviceID` | span tags `user_id` / `device_id` | 终端用户维度；context 值会覆盖单次调用中的 Config 默认值。 |
| `Config.Metadata`，或 context `WithMetadata` | span string tags | 自定义可检索元数据；context tags 会叠加 Config 默认值，保留的 trace 协议字段会被忽略。 |
| Workflow `ParentRunID` | OTel 父子关系和 `gopact.parent_run_id` | 将嵌套 Agent span 关联到父 run。 |
| Workflow `DefinitionID` | `agent_name`；嵌套 Agent span 名称 | 标识 Workflow/Agent 定义。 |
| 节点 `NodeID` | 节点 span 名称和 `gopact.node_id` | `model`、`tool` 使用对应 span type，其他值使用 `graph`。 |
| 节点 `ActivationID` / `AttemptID` | `gopact.activation_id` / `gopact.attempt_id` | 分别标识一次节点激活及其中一次执行尝试。 |
| `ToolCall.ID` | typed tool span 上的 `tool_call_id` | 标识模型请求的工具调用，不是 OTel Span ID。 |
| OTel trace、span 和 parent ID | 顶层 `trace_id`、`span_id` 和 `parent_id` | 从输入 context 继承或由 OTel 生成，不从 RunID、SessionID 派生。 |

本模块发送的是 Fornax trace-ingest JSON，不是 OTLP。内部的 `cozeloop.span_type`、`cozeloop.input`、`cozeloop.output` 和 `cozeloop.status_code` attribute 会分别转换为顶层 `span_type`、`input`、`output` 和 `status_code` 字段；其他 attribute 按类型写入 `tags_string`、`tags_long`、`tags_double` 或 `tags_bool`。

开启内容采集时，每个编码后的 input 或 output 字段最多为 4 MiB（4,194,304 bytes）。超限字段会被省略；调用级截断记录在 root span 的 `cut_off` 中，model 和 tool 节点 span 分别记录自身的截断。流式 chunk 仍会完整转发给应用。关闭内容采集时，middleware 不会为 trace 聚合流式内容。

核心 Workflow event 契约只包含生命周期元数据，不包含 provider 请求体、token 用量或模型与工具结果。存在活动 Workflow 节点时，typed model 或 tool observation 会富化该节点 span。没有活动节点时，model observation 会生成独立的 model span，tool observation 则被忽略。只有开启 `CaptureContent` 后才会上报内容 payload；adapter 没有发出的字段不会被虚构。
