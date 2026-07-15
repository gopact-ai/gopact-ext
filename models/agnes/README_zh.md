# Agnes 模型 Provider

[English documentation](README.md)

`agnes` 通过 Agnes 的 OpenAI-compatible API 实现对话、流式输出与模型发现，并接入其公开文档中的图像和异步视频 endpoint。

```go
model, err := agnes.New(os.Getenv("AGNES_API_KEY"))
if err != nil {
	return err
}

catalog, err := model.ListModels(ctx)
response, err := model.Invoke(ctx, model.NewRequest(
	gopact.UserMessage("总结这次变更。"),
))
```

对话默认使用 `agnes-2.0-flash`，因此模型发现不要求用户先猜一个 bootstrap 模型。

## 图像与视频

`GenerateImage` 同时支持文生图与图像编辑；编辑时把公开图片 URL 或 data URI 放入 `ImageRequest.Extra.Images`。`CreateVideo` 创建任务，当前流程应使用返回的 `video_id` 轮询：

```go
task, err := model.CreateVideo(ctx, agnes.VideoRequest{
	Prompt:    "镜头缓慢环绕一座玻璃雕塑，电影质感",
	Height:    768,
	Width:     1152,
	NumFrames: 121,
	FrameRate: 24,
})
result, err := model.Video(ctx, task.VideoID, task.Model)
```

`LegacyVideo` 只用于明确记录的旧 `task_id` 流程；新接入应使用 `Video` 与 `video_id`。

Agnes 当前没有公开 embedding API，也没有供 API key 查询订阅账号实时用量的 endpoint。因此本 adapter 不实现 `gopact.Embedder`，也不会暴露需要网站 session 的私有订阅接口。公开的套餐限制只是参考文档，不是账号级使用量 telemetry。

Provider 边界以 Agnes 官方的[图像生成](https://agnes-ai.com/doc/agnes-image-21-flash)、[视频生成](https://agnes-ai.com/doc/agnes-video-v20)与 [`video_id` 轮询](https://github.com/AgnesAI-Labs/AgnesAI-Models/blob/main/MODEL_CATALOG.md)说明为准。
