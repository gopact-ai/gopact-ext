# OpenAI model provider

Chinese documentation: [README_zh.md](README_zh.md)

`openai` implements `gopact.Model`, `gopact.StreamingModel`, `gopact.Embedder`, and `gopact.ModelCatalog` for the public OpenAI API. It also exposes provider-native runtime methods where a provider-neutral generation interface would discard important request or response fields.

## Setup, models, and embeddings

```go
model, err := openai.New(
	"openai",
	openai.DefaultAPIBaseURL,
	os.Getenv("OPENAI_API_KEY"),
	"", // discover before selecting a generation model
)
if err != nil {
	return err
}

catalog, err := model.ListModels(ctx)
if err != nil {
	return err
}
selectedModelID := catalog.Models[0].ID // let the user choose in a real application

request := model.NewRequest(gopact.UserMessage("Explain this change."))
request.Model = selectedModelID // selected from catalog.Models
response, err := model.Invoke(ctx, request)

vectors, err := model.Embed(ctx, gopact.EmbeddingRequest{
	Model: "text-embedding-3-small",
	Input: []string{"first document", "second document"},
})
```

An empty default generation model is valid so applications can fetch `ListModels` before asking the user to choose. Generation calls still require a model on the request. `GetModel` retrieves one model by ID. `ListModels` and `Embed` also satisfy the provider-neutral core interfaces, so an application can discover them through type assertions rather than importing this package at every call site.

In a custom model-tool loop, retain the complete `ModelResponse.Message`, execute its `ToolCalls`, and append one `tool` message per result with the originating `ToolCallID`. Outputs may be appended in any order because association is explicit.

## Runtime API surface

| Area | Methods |
| --- | --- |
| Generation | `Invoke`, `InvokeStream`, `Complete`, `StreamCompletion`, `CreateResponse`, `StreamResponse` |
| Response lifecycle | `GetResponse`, `StreamStoredResponse`, `CancelResponse`, `DeleteResponse`, `ResponseInputItems`, `CountResponseInputTokens`, `CompactResponse` |
| Models and safety | `ListModels`, `GetModel`, `Embed`, `Moderate` |
| Images | `GenerateImage`, `StreamImage`, `EditImage`, `StreamImageEdit`, `CreateImageVariation` |
| Audio | `Speech`, `StreamSpeech`, `Transcribe`, `StreamTranscription`, `Translate` |
| Videos | `CreateVideo`, `GetVideo`, `ListVideos`, `DeleteVideo`, `DownloadVideo`, `RemixVideo`, `EditVideo`, `ExtendVideo`, `CreateVideoCharacter`, `GetVideoCharacter` |
| Files | `UploadFile`, `GetFile`, `ListFiles`, `DeleteFile`, `DownloadFile` |
| Large uploads | `CreateUpload`, `AddUploadPart`, `CompleteUpload`, `CancelUpload` |

Binary-returning methods use `Media`; the caller must close `Media.Body`. Upload helpers currently accept in-memory `FileContent`, and request/response bodies and SSE frames are bounded.

The package intentionally covers inference and the upload/query/poll operations needed by those calls. Long-lived application resource management, fine-tuning, evals, and batches remain outside this adapter.

## Organization usage and costs

OpenAI organization usage requires an Admin API key, not the normal project key used by `Model`. Keep the two credentials and clients separate:

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

Supported usage categories are completions, embeddings, moderations, images, audio speech, audio transcription, vector stores, code interpreter sessions, file search calls, and web search calls. This is OpenAI API-platform organization metering; it is unrelated to the ChatGPT/Codex subscription limits returned by [`codex.Model.SubscriptionUsage`](codex/README.md).

## Upstream boundary

The API surface follows the public OpenAI references for [Responses](https://developers.openai.com/api/reference/resources/responses), [embeddings](https://developers.openai.com/api/reference/resources/embeddings/methods/create), [models](https://developers.openai.com/api/reference/resources/models/methods/list), [images](https://developers.openai.com/api/reference/resources/images), [audio](https://developers.openai.com/api/reference/resources/audio), [videos](https://developers.openai.com/api/reference/resources/videos), [files](https://developers.openai.com/api/reference/resources/files), [uploads](https://developers.openai.com/api/reference/resources/uploads), and [organization usage](https://developers.openai.com/api/reference/resources/organization/subresources/usage). Provider availability and accepted model-specific fields can change upstream.
