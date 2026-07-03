# supervisor

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/supervisor.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/supervisor)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`supervisor` routes one task to a named child `gopact.Runnable` while preserving run events and runtime IDs. Keep routing policy in the injected `Router`; child agents own their own planning, tool use, checkpointing, and retries.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/supervisor@v0.1.7` and run `(cd agents/supervisor && go test -count=1 ./...)` before changing behavior.
