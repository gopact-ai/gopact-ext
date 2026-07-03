# agenttool

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/agenttool.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/agenttool)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`agenttool` adapts an A2A agent into a standard `gopact.ToolFunc`. Use it when a parent agent should delegate work to a domain agent while preserving task events, artifacts, and runtime IDs.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.28` and run `(cd agents/agenttool && go test -count=1 ./...)` before changing behavior.
