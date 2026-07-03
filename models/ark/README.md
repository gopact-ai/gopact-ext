# ark

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/ark.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/ark)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`models/ark` is the Volcengine Ark SDK adapter for `gopact`. It uses the Ark runtime SDK and supports API key authentication as well as AK/SK authentication.

Install it with `go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.27`. If an Ark endpoint is being used as an OpenAI-compatible HTTP API, use `models/openai` instead; this module is for the Ark SDK path.
