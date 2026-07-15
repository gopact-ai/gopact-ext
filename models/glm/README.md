# GLM/Z.AI model provider

Chinese documentation: [README_zh.md](README_zh.md)

`glm` uses the Coding Plan endpoint for chat while exposing embeddings, model discovery, media, tools, files, and specialized agents from the general Z.AI API. One `Model` implements `gopact.Model`, `gopact.StreamingModel`, `gopact.Embedder`, and `gopact.ModelCatalog`.

```go
model, err := glm.New(os.Getenv("ZAI_API_KEY"))
if err != nil {
	return err
}

catalog, err := model.ListModels(ctx)
vectors, err := model.Embed(ctx, gopact.EmbeddingRequest{
	Input: []string{"first document", "second document"},
})
```

Chat defaults to `glm-5-turbo`; embeddings default to `embedding-3`. `WithChatBaseURL`, `WithAPIBaseURL`, and `WithMonitorBaseURL` configure the three upstream surfaces independently.

## Runtime API surface

| Area | Methods |
| --- | --- |
| Chat, embeddings, models | `Invoke`, `InvokeStream`, `CreateAsyncChat`, `AsyncChatResult`, `Embed`, `ListModels`, `GetModel` |
| Images and video | `GenerateImage`, `CreateImage`, `CreateVideo`, `AsyncResult` |
| Audio | `Transcribe`, `StreamTranscription`, `Speech`, `StreamSpeech`, `CustomizeSpeech` |
| Moderation | `Moderate` |
| Tools | `Tokenize`, `ParseLayout`, `Search`, `ReadURL`, `SearchTool`, `StreamSearchTool` |
| Files and document parsing | `UploadFile`, `ListFiles`, `DeleteFile`, `DownloadFile`, `CreateFileParserTask`, `ParseFile`, `FileParserResult`, `RecognizeHandwriting` |
| Specialized agents | `RunAgent`, `StreamAgent`, `AgentResult`, `AgentConversation` |

Image and video tasks use `AsyncResult` for polling; asynchronous chat uses `AsyncChatResult`. `Search` exposes the structured `/web_search` endpoint, while `SearchTool` and `StreamSearchTool` expose the model-driven `/tools` contract. Transcription accepts exactly one in-memory `FileContent` or base64 string. Binary audio, file content, and parser-result methods return `Media`; callers must close its `Body`. Specialized-agent choices stay as `json.RawMessage` because translation, video-template, slide, and poster agents return different shapes.

## Coding Plan usage

```go
quota, err := model.PlanUsage(ctx)

period := glm.UsagePeriod{
	Start: time.Now().Add(-7 * 24 * time.Hour),
	End:   time.Now(),
}
modelUsage, err := model.ModelUsage(ctx, period)
toolUsage, err := model.ToolUsage(ctx, period)
```

`PlanUsage` reports active quota windows and reset times. `ModelUsage` reports calls and tokens by model; `ToolUsage` reports network search, web-reader, repository-reader, and other metered tool totals. These endpoints use the Coding Plan monitor contract and its raw-token `Authorization` format internally.

The runtime shapes follow the public [Z.AI API reference](https://docs.z.ai/api-reference/introduction) and [official Python SDK](https://github.com/zai-org/z-ai-sdk-python). Long-lived training, batch, assistant, knowledge-base, and voice-clone resource management are intentionally outside this runtime adapter. Coding Plan availability, quota fields, and model catalogs can change upstream.
