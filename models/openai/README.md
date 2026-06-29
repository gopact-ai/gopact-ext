# openai

OpenAI-shaped Chat Completions and Responses provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/openai@v0.3.2
```

## Usage

```go
client, err := openai.NewClient(
	"openrouter",
	"https://openrouter.ai/api/v1",
	appSecrets.OpenRouterAPIKey,
	openai.WithChatCompletionsAPI(),
	openai.WithMaxOutputTokens(1024),
	openai.WithThinkingType("enabled"),
	openai.WithModel("openai/gpt-4o-mini", openai.CapabilityToolCalling),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.ModelRequest{
	Model: "openai/gpt-4o-mini",
	Messages: []gopact.Message{{
		Role:    gopact.RoleUser,
		Content: "Say hello",
	}},
})
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
- `MaxOutputTokens`, request `Budget.MaxOutputTokens`, `Temperature`, `TopP`, `ThinkingType`, and `ReasoningEffort`.
- Text and image content parts for Responses requests.
- Reasoning summaries mapped to `gopact.ContentPartReasoning`.
- Usage metadata and provider error classification.
- Provider conformance tests.
