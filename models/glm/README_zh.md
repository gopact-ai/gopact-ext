# GLM/Z.AI 模型 Provider

[English documentation](README.md)

`glm` 使用 Coding Plan endpoint 处理对话，同时通过 Z.AI 通用 API 提供 embedding、模型发现、媒体、工具、文件与专用 Agent。一个 `Model` 同时实现 `gopact.Model`、`gopact.StreamingModel`、`gopact.Embedder` 与 `gopact.ModelCatalog`。

```go
model, err := glm.New(os.Getenv("ZAI_API_KEY"))
if err != nil {
	return err
}

catalog, err := model.ListModels(ctx)
vectors, err := model.Embed(ctx, gopact.EmbeddingRequest{
	Input: []string{"第一篇文档", "第二篇文档"},
})
```

对话默认使用 `glm-5-turbo`，embedding 默认使用 `embedding-3`。`WithChatBaseURL`、`WithAPIBaseURL` 与 `WithMonitorBaseURL` 可分别配置三套上游入口。

## Runtime API 范围

| 领域 | 方法 |
| --- | --- |
| 对话、embedding、模型 | `Invoke`、`InvokeStream`、`CreateAsyncChat`、`AsyncChatResult`、`Embed`、`ListModels`、`GetModel` |
| 图像与视频 | `GenerateImage`、`CreateImage`、`CreateVideo`、`AsyncResult` |
| 语音 | `Transcribe`、`StreamTranscription`、`Speech`、`StreamSpeech`、`CustomizeSpeech` |
| 审核 | `Moderate` |
| 工具 | `Tokenize`、`ParseLayout`、`Search`、`ReadURL`、`SearchTool`、`StreamSearchTool` |
| 文件与文档解析 | `UploadFile`、`ListFiles`、`DeleteFile`、`DownloadFile`、`CreateFileParserTask`、`ParseFile`、`FileParserResult`、`RecognizeHandwriting` |
| 专用 Agent | `RunAgent`、`StreamAgent`、`AgentResult`、`AgentConversation` |

图像和视频异步任务通过 `AsyncResult` 轮询，异步对话通过 `AsyncChatResult` 轮询。`Search` 对应结构化的 `/web_search` endpoint，`SearchTool` 与 `StreamSearchTool` 对应模型驱动的 `/tools` 契约。转写输入必须在内存 `FileContent` 与 base64 字符串中二选一。二进制语音、文件内容和解析结果方法返回 `Media`，调用方必须关闭其 `Body`。翻译、视频模板、幻灯片和海报 Agent 的返回 shape 不同，因此专用 Agent 的 choices 保留为 `json.RawMessage`。

## Coding Plan 用量

```go
quota, err := model.PlanUsage(ctx)

period := glm.UsagePeriod{
	Start: time.Now().Add(-7 * 24 * time.Hour),
	End:   time.Now(),
}
modelUsage, err := model.ModelUsage(ctx, period)
toolUsage, err := model.ToolUsage(ctx, period)
```

`PlanUsage` 返回当前配额窗口与重置时间；`ModelUsage` 按模型返回调用次数与 token；`ToolUsage` 返回联网搜索、网页读取、仓库读取及其他计量工具的总量。内部会按 Coding Plan monitor 契约使用 raw-token `Authorization` 格式。

Runtime shape 以公开的 [Z.AI API 文档](https://docs.z.ai/api-reference/introduction)和[官方 Python SDK](https://github.com/zai-org/z-ai-sdk-python)为准。训练、batch、assistant、知识库和 voice clone 等长生命周期资源管理有意不进入这个 runtime adapter。Coding Plan 可用性、配额字段和模型目录可能随上游变化。
