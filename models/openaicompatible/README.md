# openaicompatible

OpenAI-compatible Chat Completions provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/openaicompatible
```

## Usage

```go
client, err := openaicompatible.New(openaicompatible.Options{
	Provider: "openrouter",
	BaseURL:  "https://openrouter.ai/api/v1",
	APIKey:   appSecrets.OpenRouterAPIKey,
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

`BaseURL` should point at an OpenAI-compatible `/v1` API root. The adapter posts to `BaseURL + "/chat/completions"`.
