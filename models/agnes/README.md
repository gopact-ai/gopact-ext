# agnes

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/agnes.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/agnes)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`models/agnes` adapts Agnes AI to the `gopact` model contract. Agnes exposes an OpenAI-compatible Chat Completions API, so this module wraps `models/openai` with Agnes defaults and a provider-specific thinking toggle.

Install it with `go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.27`. Use `New` for the default Agnes endpoint or `NewClient` when tests need a mock server or a custom gateway.
