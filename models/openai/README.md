# openai

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/openai.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/openai)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`models/openai` adapts OpenAI-shaped APIs to the `gopact` model contract. It supports both Chat Completions and Responses, including generate, streaming, tool calls, structured output, reasoning/thinking controls, usage metadata, cancellation, timeout handling, and provider error classification.

Install it with `go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.20`. API paths are selected by `WithChatCompletionsAPI` or `WithResponsesAPI`; application code should pass a base URL, not concatenate endpoint paths.
