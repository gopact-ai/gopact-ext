# openai

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`models/openai` 是 OpenAI-shaped provider adapter。它面向 OpenAI 官方 API 以及兼容 OpenAI request/response 形态的服务，支持 Chat Completions 和 Responses 两种 API。

API path 在模块内部固定：

- `openai.WithChatCompletionsAPI()` -> `/chat/completions`
- `openai.WithResponsesAPI()` -> `/responses`

调用方只需要提供 provider name、base URL、token、默认 model 和请求参数，不应该在 example 或业务代码里拼 API path。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.21
```

## Chat Completions

```go
client, err := openai.NewClient(
	openai.ProviderOpenAI,
	os.Getenv("GOPACT_LLM_BASEURL"),
	os.Getenv("GOPACT_LLM_TOKEN"),
	openai.WithChatCompletionsAPI(),
	gopact.WithModel(os.Getenv("GOPACT_LLM_MODEL")),
	gopact.EnableStreaming(),
	gopact.EnableToolCalling(),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.UserMessage("Reply with one concise sentence.")),
	gopact.WithMaxOutputTokens(512),
	gopact.WithTemperature(0.2),
))
```

## Responses

```go
client, err := openai.NewClient(
	openai.ProviderOpenAI,
	os.Getenv("GOPACT_LLM_BASEURL"),
	os.Getenv("GOPACT_LLM_TOKEN"),
	openai.WithResponsesAPI(),
	gopact.WithModel(os.Getenv("GOPACT_LLM_MODEL")),
	gopact.EnableStreaming(),
	gopact.EnableReasoning(),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.UserMessage("Explain the status in JSON.")),
	gopact.WithResponseSchema(schema),
	gopact.WithMaxOutputTokens(1024),
))
```

## Streaming

`Stream` 使用同一个 `gopact.ModelRequest` 契约。请求级参数可以覆盖 client 默认值：

```go
for event, err := range client.Stream(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.UserMessage("Stream a short answer.")),
	gopact.WithMaxOutputTokens(512),
	gopact.WithTemperature(0.1),
)) {
	if err != nil {
		return err
	}
	_ = event
}
```

## 能力

支持：

- Chat Completions 和 Responses 的 `Generate`。
- Chat Completions 和 Responses 的 SSE `Stream`。
- `gopact.ModelRequest` 的 model、budget、sampling、tools、tool choice、structured output、thinking 和 reasoning 参数。
- tool definitions、assistant `tool_calls`、streamed tool call argument aggregation。
- Responses text/image input、function call output、reasoning summary。
- OpenAI-compatible Chat Completions provider 的 `chat_template_kwargs`。
- usage metadata、timeout/cancel 和 provider error classification。

## 验证

```bash
(cd models/openai && go test -count=1 ./...)
(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)
```
