# Agnes model provider

Chinese documentation: [README_zh.md](README_zh.md)

`agnes` implements chat, streaming, and model discovery through Agnes's OpenAI-compatible API, plus its documented image and asynchronous-video endpoints.

```go
model, err := agnes.New(os.Getenv("AGNES_API_KEY"))
if err != nil {
	return err
}

catalog, err := model.ListModels(ctx)
response, err := model.Invoke(ctx, model.NewRequest(
	gopact.UserMessage("Summarize this change."),
))
```

The chat model defaults to `agnes-2.0-flash`, so discovery does not require the user to guess a bootstrap model.

## Images and video

`GenerateImage` covers both text-to-image and image editing. Put public image URLs or data URIs in `ImageRequest.Extra.Images` for editing. `CreateVideo` starts a task; poll current tasks with the returned `video_id`:

```go
task, err := model.CreateVideo(ctx, agnes.VideoRequest{
	Prompt:    "A slow cinematic orbit around a glass sculpture",
	Height:    768,
	Width:     1152,
	NumFrames: 121,
	FrameRate: 24,
})
result, err := model.Video(ctx, task.VideoID, task.Model)
```

`LegacyVideo` exists only for explicitly documented old `task_id` workflows. New integrations should use `Video` and `video_id`.

Agnes currently documents no public embedding API and no API-key endpoint for reading a subscriber's live usage. Consequently this adapter does not implement `gopact.Embedder` and does not expose the private website-session subscription endpoint. Published plan limits are reference documentation, not account-specific usage telemetry.

The provider boundary follows the official Agnes references for [image generation](https://agnes-ai.com/doc/agnes-image-21-flash), [video generation](https://agnes-ai.com/doc/agnes-video-v20), and [`video_id` polling](https://github.com/AgnesAI-Labs/AgnesAI-Models/blob/main/MODEL_CATALOG.md).
