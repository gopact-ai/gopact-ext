# agenttool

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/agenttool.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/agenttool)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: zh,en -->

## 中文

`agenttool` 将 A2A agent 包装成普通 `gopact.ToolFunc`。它适合父 agent 需要把某个垂域 agent 当作工具调用的场景，例如 ReAct agent 委托 Plan-Execute agent 完成规划。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.14
```

## 用法

```go
child, err := a2a.NewRunnableAgent(a2a.AgentCard{
	Name:        "planner",
	Description: "Plan a task and return execution evidence.",
}, plannerAgent)
if err != nil {
	return err
}

tool, err := agenttool.New(
	child,
	agenttool.WithName("delegate_plan"),
	agenttool.WithDescription("Delegate planning to the planner agent."),
)
if err != nil {
	return err
}
```

默认 tool schema 支持：

- `input`：必填，传给子 agent 的任务输入。
- `task_id`：可选，指定子 A2A task id。
- `metadata`：可选，透传给子 task 的元数据。

如果子 agent 支持 streaming，`agenttool` 会把 A2A message、artifact、completion 和 failure evidence 合并到 `gopact.ToolResult.Events`，父 agent 可以继续记录或验证这些事件。

## 验证

```bash
(cd agents/agenttool && go test -count=1 ./...)
```

## English

`agenttool` adapts an A2A agent into a standard `gopact.ToolFunc`. Use it when a parent agent should delegate work to a domain agent while preserving task events, artifacts, and runtime IDs.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.14` and run `(cd agents/agenttool && go test -count=1 ./...)` before changing behavior.
