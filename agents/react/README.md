# react

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/react.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/react)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)


<!-- gopact:doc-language: zh,en -->

## 中文

本文档是 gopact 开源文档集的一部分，中文内容用于说明当前仓库约束、能力或维护流程。

## English

This document is part of the gopact open-source documentation set. The English section gives an entry point for readers who prefer English, while the remaining sections preserve the maintained technical details.


ReAct-style model/tool loop agent template for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.13
```

## Scope

This module externalizes the ReAct template from core. It keeps the template provider-neutral: callers can pass any `gopact.ResponseModel`.

## Usage

```go
agent, err := react.NewModelAgent(
	model,
	react.WithTools(ctx, uppercaseTool),
	react.WithModelOptions(
		gopact.WithMaxOutputTokens(1024),
		gopact.WithTemperature(0.2),
	),
)
if err != nil {
	return err
}

for event, err := range agent.Run(ctx, "uppercase gopact and answer briefly") {
	if err != nil {
		return err
	}
	_ = event
}
```

Advanced callers can still use `New` with a custom `gopact.ChatModel` and `tools.Registry`.
