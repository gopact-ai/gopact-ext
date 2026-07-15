# OpenAI 模型 Provider

[English documentation](README.md)

`openai` 面向公开 OpenAI API 实现 `gopact.Model`、`gopact.StreamingModel`、`gopact.Embedder` 与 `gopact.ModelCatalog`。对于无法无损映射到 provider-neutral 生成接口的能力，本包同时提供 provider-native runtime 方法。

## 初始化、模型发现与 embedding

```go
model, err := openai.New(
	"openai",
	openai.DefaultAPIBaseURL,
	os.Getenv("OPENAI_API_KEY"),
	"", // 先发现模型，再选择生成模型
)
if err != nil {
	return err
}

catalog, err := model.ListModels(ctx)
if err != nil {
	return err
}
selectedModelID := catalog.Models[0].ID // 实际应用应让用户选择

request := model.NewRequest(gopact.UserMessage("解释这次变更。"))
request.Model = selectedModelID // 从 catalog.Models 中选择
response, err := model.Invoke(ctx, request)

vectors, err := model.Embed(ctx, gopact.EmbeddingRequest{
	Model: "text-embedding-3-small",
	Input: []string{"第一篇文档", "第二篇文档"},
})
```

默认生成模型可以留空，让应用在询问用户前先调用 `ListModels`；真正发起生成时仍必须在 request 上设置模型。`GetModel` 可按 ID 读取单个模型。`ListModels` 与 `Embed` 同时满足 core 的 provider-neutral 接口，应用可以通过类型断言发现能力，不必让每个调用点都依赖本包。

## Runtime API 范围

| 领域 | 方法 |
| --- | --- |
| 生成 | `Invoke`、`InvokeStream`、`Complete`、`StreamCompletion`、`CreateResponse`、`StreamResponse` |
| Response 生命周期 | `GetResponse`、`StreamStoredResponse`、`CancelResponse`、`DeleteResponse`、`ResponseInputItems`、`CountResponseInputTokens`、`CompactResponse` |
| 模型与安全 | `ListModels`、`GetModel`、`Embed`、`Moderate` |
| 图像 | `GenerateImage`、`StreamImage`、`EditImage`、`StreamImageEdit`、`CreateImageVariation` |
| 语音 | `Speech`、`StreamSpeech`、`Transcribe`、`StreamTranscription`、`Translate` |
| 视频 | `CreateVideo`、`GetVideo`、`ListVideos`、`DeleteVideo`、`DownloadVideo`、`RemixVideo`、`EditVideo`、`ExtendVideo`、`CreateVideoCharacter`、`GetVideoCharacter` |
| 文件 | `UploadFile`、`GetFile`、`ListFiles`、`DeleteFile`、`DownloadFile` |
| 大文件分片上传 | `CreateUpload`、`AddUploadPart`、`CompleteUpload`、`CancelUpload` |

返回二进制内容的方法使用 `Media`，调用方必须关闭 `Media.Body`。上传 helper 当前接受内存中的 `FileContent`；请求、响应和 SSE frame 都有大小上限。

本包有意只覆盖推理，以及推理必需的上传、查询和轮询操作；长期应用资源管理、fine-tuning、eval 与 batch 不在此 adapter 范围内。

## 组织用量与成本

OpenAI 组织用量需要 Admin API key，不能使用 `Model` 的普通 project key。两套凭据和 client 应分开保存：

```go
admin, err := openai.NewAdminClient(os.Getenv("OPENAI_ADMIN_KEY"))
if err != nil {
	return err
}

usage, err := admin.Usage(ctx, openai.OrganizationUsageCompletions,
	openai.OrganizationUsageQuery{
		StartTime: time.Now().Add(-24 * time.Hour),
		BucketWidth: "1h",
	},
)
costs, err := admin.Costs(ctx, openai.OrganizationCostsQuery{
	StartTime: time.Now().Add(-24 * time.Hour),
})
```

支持 completions、embeddings、moderations、images、audio speech、audio transcription、vector stores、code interpreter sessions、file search calls 与 web search calls。这里查询的是 OpenAI API 平台的组织计量，不是 [`codex.Model.SubscriptionUsage`](codex/README_zh.md) 返回的 ChatGPT/Codex 订阅限制。

## 上游边界

API 范围以 OpenAI 公开文档中的 [Responses](https://developers.openai.com/api/reference/resources/responses)、[embeddings](https://developers.openai.com/api/reference/resources/embeddings/methods/create)、[models](https://developers.openai.com/api/reference/resources/models/methods/list)、[images](https://developers.openai.com/api/reference/resources/images)、[audio](https://developers.openai.com/api/reference/resources/audio)、[videos](https://developers.openai.com/api/reference/resources/videos)、[files](https://developers.openai.com/api/reference/resources/files)、[uploads](https://developers.openai.com/api/reference/resources/uploads) 与 [organization usage](https://developers.openai.com/api/reference/resources/organization/subresources/usage) 为准。Provider 可用性和各模型接受的字段可能随上游变化。
