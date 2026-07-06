# GLM Provider

[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/glm.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/glm)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`models/glm` adapts GLM/Zhipu AI OpenAI-compatible Chat Completions APIs to the `gopact` model contract. It wraps `models/openai` with provider-specific defaults for the China Open Platform and the international Z.AI Coding Plan endpoint.

Install it with `go get github.com/gopact-ai/gopact-ext/models/glm@v0.1.0`.

## Endpoints

- China: `glm.New` or `glm.NewClient`, defaulting to `glm.DefaultBaseURL`.
- International: `glm.NewInternational` or `glm.NewInternationalClient`, defaulting to `glm.DefaultInternationalBaseURL`.

Both profiles use OpenAI-compatible Chat Completions. Application code should pass a model through configuration instead of hard-coding deployment-specific model IDs.

## Usage

```go
client, err := glm.NewInternational(
	os.Getenv("GOPACT_GLM_INTERNATIONAL_API_KEY"),
	gopact.WithModel(os.Getenv("GOPACT_GLM_MODEL")),
	gopact.EnableStreaming(),
	glm.DisableThinking(),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.UserMessage("Reply with one sentence.")),
	gopact.WithMaxOutputTokens(512),
	gopact.WithTemperature(0.2),
))
```

Use `NewClient` or `NewInternationalClient` when tests need a mock server or a custom gateway.

## Integration Tests

Real provider tests are opt-in and require the `integration` build tag:

```bash
(cd models/glm && go test -tags=integration -count=1 ./...)
```

Environment variables:

- `GOPACT_GLM_API_KEY`: China Open Platform API key.
- `GOPACT_GLM_BASEURL`: optional China endpoint override.
- `GOPACT_GLM_INTERNATIONAL_API_KEY`: international Z.AI API key.
- `GOPACT_GLM_INTERNATIONAL_BASEURL`: optional international endpoint override.
- `GOPACT_GLM_MODEL`: model configured by the caller.

The integration tests skip when the required API key or model is not set.
