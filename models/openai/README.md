# openai

OpenAI-shaped Chat Completions and Responses provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.12
```

## Usage

```go
client, err := openai.NewClient(
	"openrouter",
	"https://openrouter.ai/api/v1",
	appSecrets.OpenRouterAPIKey,
	openai.WithChatCompletionsAPI(),
	gopact.WithModel("openai/gpt-4o-mini"),
	gopact.WithMaxOutputTokens(1024),
	openai.EnableThinking(),
	gopact.EnableToolCalling(),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.Message{
		Role:    gopact.RoleUser,
		Content: "Say hello",
	}),
	gopact.WithTemperature(0.2),
))
if err != nil {
	return err
}
fmt.Println(response.Message.Text())
```

`BaseURL` should point at an OpenAI-compatible API root. API paths are selected inside this package: `WithChatCompletionsAPI()` posts to chat completions, and `WithResponsesAPI()` posts to responses.

## Scope

Supported:

- Chat Completions and Responses via `Generate`.
- SSE streaming via `Stream` for Chat Completions and Responses.
- Tool definitions, assistant `tool_calls`, and streamed function call arguments.
- `gopact.ModelRequest` options for model, capabilities, budget, sampling, thinking, and reasoning parameters.
- Provider-specific `chat_template_kwargs` for OpenAI-compatible Chat Completions providers.
- Text and image content parts for Responses requests.
- Reasoning summaries mapped to `gopact.ContentPartReasoning`.
- Usage metadata and provider error classification.
- Provider conformance tests.
