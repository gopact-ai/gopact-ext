# openai

OpenAI-shaped Chat Completions and Responses provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/openai@v0.3.0
```

## Usage

```go
client, err := openai.New(openai.Options{
	Provider:        "openrouter",
	BaseURL:         "https://openrouter.ai/api/v1",
	APIKey:          appSecrets.OpenRouterAPIKey,
	API:             openai.APIChatCompletions,
	MaxOutputTokens: 1024,
	ThinkingType:    "enabled",
	Models: []provider.ModelInfo{{
		Name:         "openai/gpt-4o-mini",
		Provider:     "openrouter",
		Capabilities: []provider.Capability{provider.CapabilityToolCalling},
	}},
})
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

`BaseURL` should point at an OpenAI-compatible `/v1` API root. `API` defaults to `openai.APIChatCompletions`; set `openai.APIResponses` to post to `BaseURL + "/responses"`.

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
