# agenttool

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
