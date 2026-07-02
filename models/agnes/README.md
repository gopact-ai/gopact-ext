# agnes

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/agnes.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/agnes)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)


<!-- gopact:doc-language: zh,en -->

## 中文

本文档是 gopact 开源文档集的一部分，中文内容用于说明当前仓库约束、能力或维护流程。

## English

This document is part of the gopact open-source documentation set. The English section gives an entry point for readers who prefer English, while the remaining sections preserve the maintained technical details.


Agnes AI text model provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.16
```

## Usage

```go
client, err := agnes.New(
	appSecrets.AgnesAPIKey,
	agnes.EnableThinking(),
	gopact.WithMaxOutputTokens(1024),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.Message{
		Role:    gopact.RoleUser,
		Content: "Say hello",
	}),
))
if err != nil {
	return err
}
fmt.Println(response.Message.Text())
```

`DefaultBaseURL` is `https://apihub.agnes-ai.com/v1`, and `DefaultModel` is `agnes-2.0-flash`.
Thinking is sent as `chat_template_kwargs.enable_thinking` for Agnes OpenAI-compatible Chat Completions.
