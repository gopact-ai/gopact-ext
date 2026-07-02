# agenttool

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/agenttool.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/agenttool)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)


<!-- gopact:doc-language: zh,en -->

## 中文

本文档是 gopact 开源文档集的一部分，中文内容用于说明当前仓库约束、能力或维护流程。

## English

This document is part of the gopact open-source documentation set. The English section gives an entry point for readers who prefer English, while the remaining sections preserve the maintained technical details.


`agenttool` adapts an A2A agent into a standard `gopact.ToolFunc`.

```go
child, err := a2a.NewRunnableAgent(a2a.AgentCard{Name: "planner"}, plannerAgent)
if err != nil {
	return err
}
tool, err := agenttool.New(child, agenttool.WithName("delegate_plan"))
if err != nil {
	return err
}
```

The default tool input schema accepts:

- `input`: required child task input.
- `task_id`: optional child A2A task id.

Streaming child agents preserve A2A message, artifact, completion, and failure evidence in `gopact.ToolResult.Events`.
