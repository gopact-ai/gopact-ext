# react

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/react.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/react)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`react` is a ReAct-style model/tool loop template. Use it when the model should choose visible tools, observe results, and either continue the loop or produce a final answer.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.25`. The template is provider-neutral, supports local tools, memory hooks, checkpoint/resume, artifact verification, and final run verification.
