# ByteDance Fornax 中间件

[英文文档](README.md)

`fornax` 是 ByteDance 专属中间件，用于包装 `gopact` Agent，并把 Agent、Workflow 与节点 span 上报到 Fornax 链路接收地址。

配置采用显式注入。中间件不从环境变量读取凭据；应用自行决定这些参数的来源和管理方式。

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

默认只上报运行元数据，不上报 Agent 消息、模型请求与响应、工具参数、结果摘要、流式输出或详细错误信息。

## 完整配置

```go
middleware, err := fornax.New(ctx, fornax.Config{
	AK:      ak,
	SK:      sk,
	SpaceID: "12345", // 可选；校验 AK/SK 鉴权得到的工作空间
	Region: "CN", // 可选；还支持 BOE、SG、BOEI18N、US、Asia-SouthEastBD 和 I18N-DEV
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

只有应用明确批准把请求与响应内容导出到 Fornax 时，才设置 `CaptureContent: true`。开启后，导出器可能填充根 span 和 Agent span 的顶层 `input`、`output` 字段；收到相应组件事件时，也会填充模型或工具 span。内容可能包括消息、工具定义与参数、结果摘要，以及聚合后的流式输出。原始错误会写入 `tags_string["error"]`。该配置的零值为 `false`，不支持按单次请求覆盖。

应用主动放入 `Config.Metadata`、`agent.Request.Metadata` 或 `WithMetadata`，且键名非空、未被保留的值，会作为标签上报，不受 `CaptureContent` 控制。非空的 `UserID` 和 `DeviceID` 同样不受该配置控制。

关闭内容采集时仍保留运行元数据：span 层级、运行、会话和节点标识、模型与工具名称、工具调用 ID、令牌用量、结束原因、错误状态、耗时，以及应用显式提供的标签。原始错误仍会返回给应用。

## 单次请求标签

`Config` 中的 `UserID`、`DeviceID` 和 `Metadata` 是默认值。`agent.Request.Metadata` 也会作为本次调用的字符串标签上报。身份或元数据随请求变化时，使用上下文辅助函数：

```go
ctx = fornax.WithUserID(ctx, "user-456")
ctx = fornax.WithDeviceID(ctx, "device-456")
ctx = fornax.WithMetadata(ctx, map[string]string{"request_id": "req-1"})
response, err := tracedAgent.Invoke(ctx, request)
```

链路协议使用的元数据键会被忽略，包括以 `cozeloop.`、`gopact.`、`fornax_` 开头的键，以及以下没有前缀的键：`agent_name`、`cut_off`、`device_id`、`duration`、`error`、`finish_reason`、`input_tokens`、`language`、`message_id`、`model_name`、`output_tokens`、`psm`、`thread_id`、`tokens`、`tool_call_id`、`tool_name` 和 `user_id`。服务与用户身份请通过 `Config.PSM`、`Config.UserID`、`Config.DeviceID`、`WithUserID` 和 `WithDeviceID` 传入；调用标识请使用 `gopact.WithRunID` 和 `gopact.WithSessionID`。组件、用量、截断和错误字段由中间件根据运行事件填写，不从自定义元数据读取。旧代码需要把保留字段迁到对应的专用配置项或函数；如果只是自定义标签与保留字段重名，请换成应用自己的键名。

每次调用都会合并 `Config.Metadata`、`agent.Request.Metadata` 和 `WithMetadata`。出现同名键时，`WithMetadata` 优先于 `agent.Request.Metadata`，`agent.Request.Metadata` 又优先于 `Config.Metadata`。三处来源合计最多为每个 span 选择 64 个不同的自定义元数据键；超出时仍按上述来源顺序保留，同一来源再按键名字典序稳定选择。64 个键只是自定义元数据的预算。OpenTelemetry 的总属性上限还会计算协议字段和运行时字段；如果把 `OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT` 设得过低，自定义标签和后写入的运行时字段都可能丢失。

如果目标 Agent 的动态类型实现了 `agent.StreamingAgent`，`Use` 会保留 `InvokeStream`；当目标 Agent 的静态类型就是 `agent.StreamingAgent` 时，可直接使用 `UseStreaming`。两种入口都会持续追踪，直到流正常结束、失败或被使用方取消。

`AK` 和 `SK` 是 Fornax 空间凭据。`Region` 可选，用于选择鉴权地址和默认链路接收地址；支持 `CN`、`BOE`、`SG`、`BOEI18N`、`US`、`Asia-SouthEastBD` 和 `I18N-DEV`，空值或未知值按 `CN` 处理。`SpaceID` 可选；传入时必须与 AK/SK 鉴权得到的空间一致。`Endpoint` 用于完整覆盖 Fornax 链路接收地址，鉴权地址仍由 `Region` 决定。`PSM` 会写入 Fornax 鉴权请求，并作为 span 的 `service_name` 和标签 `psm` 上报；未传时默认 `unknown_psm`，与 Fornax SDK 的兜底行为一致。`UserID`、`DeviceID` 和被接受的 `Metadata` 条目会作为字符串标签附加到所有上报 span，也可以通过 `WithUserID`、`WithDeviceID` 和 `WithMetadata` 按请求覆盖。

Agent 调用上报为 `fornax_query`，其下包含一个 `Agent` span。非空的 `gopact.WithRunID` 会把 `message_id` 写入所有上报 span；非空的 `gopact.WithSessionID` 会把 `thread_id` 写入所有上报 span。两者都优先于生命周期事件。如果调用方没有传入其中一项，首个根 Workflow 生命周期事件会在对应值非空时，为根 span、Agent span 和后续 span 补齐这项标识。每个 Workflow 的实际运行标识始终单独写入 `gopact.run_id`。嵌套 Workflow 运行上报为 `Agent`；名为 `model` 和 `tool` 的节点分别使用对应的 Fornax span 类型，其他节点使用 `graph`。传给 `Invoke` 的已有事件接收器会继续生效。应用退出时调用 `Close`，刷新尚未上报的 span。

## ID 对应关系

| 来源 | 上报值 | 在 Fornax 中的含义 |
| --- | --- | --- |
| AK/SK 鉴权得到的工作空间 | 链路接收数据中的 `workspace_id` | 目标工作空间，不是 trace ID 或 span ID。 |
| 通过 `gopact.WithRunID` 传入的调用 `RunID` | 所有上报 span 的 `tags_string["message_id"]` | Fornax 消息 ID；优先于生命周期事件中的 RunID。 |
| 通过 `gopact.WithSessionID` 传入的调用 `SessionID` | 所有上报 span 的 `tags_string["thread_id"]` | 把相关调用归为一组；优先于生命周期事件中的 SessionID。 |
| Workflow 生命周期事件中的 `RunID` | `tags_string["gopact.run_id"]`；首个根事件还会补齐缺少的 `message_id` | 标识根或子 Workflow 运行；嵌套运行不会改变调用级 `message_id`。 |
| Workflow 生命周期事件中的 `SessionID` | 首个根事件会在 SessionID 非空时补齐缺少的 `tags_string["thread_id"]` | 仅在调用方没有传入 `gopact.WithSessionID` 时为本次调用分组。 |
| `Config.PSM` | 鉴权 `psm`、span 的 `service_name` 和标签 `psm` | 上报服务身份；默认 `unknown_psm`。 |
| `Config.UserID` / `Config.DeviceID`，或上下文中的 `WithUserID` / `WithDeviceID` | span 标签 `user_id` / `device_id` | 终端用户维度；上下文中的值会覆盖本次调用的 Config 默认值。 |
| `Config.Metadata`、`agent.Request.Metadata`，或上下文中的 `WithMetadata` | span 字符串标签 | 可检索的自定义元数据；优先级依次为 `WithMetadata`、`agent.Request.Metadata`、`Config.Metadata`，链路协议保留键会被忽略。 |
| Workflow `ParentRunID` | OpenTelemetry 父子关系和 `gopact.parent_run_id` | 将嵌套 Agent span 关联到父运行。 |
| Workflow `DefinitionID` | `agent_name`；嵌套 Agent span 名称 | 标识 Workflow/Agent 定义。 |
| 节点 `NodeID` | 节点 span 名称和 `gopact.node_id` | `model`、`tool` 使用对应 span 类型，其他值使用 `graph`。 |
| 节点 `ActivationID` / `AttemptID` | `gopact.activation_id` / `gopact.attempt_id` | 分别标识一次节点激活及其中一次执行尝试。 |
| `ToolCall.ID` | `tool` 类型 span 上的 `tool_call_id` | 标识模型请求的工具调用，不是 OpenTelemetry span ID。 |
| OpenTelemetry trace、span 和父 span ID | 顶层 `trace_id`、`span_id` 和 `parent_id` | 从输入上下文继承或由 OpenTelemetry 生成，不从 RunID、SessionID 派生。 |

本模块发送的是 Fornax trace-ingest JSON，不是 OTLP。内部的 `cozeloop.span_type`、`cozeloop.input`、`cozeloop.output` 和 `cozeloop.status_code` 属性会分别转换为顶层 `span_type`、`input`、`output` 和 `status_code` 字段；其他属性按类型写入 `tags_string`、`tags_long`、`tags_double` 或 `tags_bool`。

开启内容采集时，每个编码后的 `input` 或 `output` 字段最多为 4 MiB（4,194,304 字节）。非流式字段超限时会被省略。流式输出会在下一个数据块超出预算前停止聚合；已经聚合的前缀只有在编码后仍未超限时才会上报。调用级截断记录在根 span 的 `tags_string["cut_off"]` 中，模型和工具节点 span 分别记录自身的截断。所有流式数据块仍会完整转发给应用。关闭内容采集时，中间件不会为了链路上报而聚合流式内容。

核心 Workflow 事件契约只包含生命周期元数据，不包含模型服务请求体、令牌用量或模型与工具结果。存在活动 Workflow 节点时，类型化的模型或工具观测事件会补充该节点 span。没有活动节点时，模型观测事件会生成独立的模型 span，工具观测事件则被忽略。只有开启 `CaptureContent` 后才会上报内容；适配器没有发出的字段不会被虚构。
