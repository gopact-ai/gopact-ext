# agenttool

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`agenttool` 将 A2A agent 包装成普通 `gopact.ToolFunc`。它适合父 agent 需要把某个垂域 agent 当作工具调用的场景，例如 ReAct agent 委托 Plan-Execute agent 完成规划。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.23
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
