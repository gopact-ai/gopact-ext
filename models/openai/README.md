# openai

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/openai.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/openai)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)


<!-- gopact:doc-language: zh,en -->

## 中文

本文档是 gopact 开源文档集的一部分，中文内容用于说明当前仓库约束、能力或维护流程。

## English

This document is part of the gopact open-source documentation set. The English section gives an entry point for readers who prefer English, while the remaining sections preserve the maintained technical details.


OpenAI-shaped Chat Completions and Responses provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.15
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
