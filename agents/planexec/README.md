# planexec

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/planexec.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/planexec)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`planexec` is a provider-neutral Plan-Execute template. It plans a task into steps, executes them, summarizes the result, and supports replan, approval interrupts, checkpoint resume, and cancellation propagation.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.16`. Use `NewModelAgent` for model-backed planning/execution or `New` when you want to provide custom `Planner`, `Executor`, and `Replanner` implementations.
