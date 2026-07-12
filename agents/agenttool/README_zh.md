# Agent Tool

<!-- gopact:doc-language: zh -->

[English](./README.md)

`agenttool` 把一个 child `agent.Agent` 适配为模型可见的 `agent.InvokableTool`，不会创建第二套 runtime。

## 常见场景

当模型必须通过 tool 边界调用已有 child Agent，且需要显式映射 tool 请求与响应时使用。

## 执行模型

`Invoke` 把 `ToolCall` 映射为 `agent.Request`，将 Workflow child options 传给 Agent，再把 `agent.Response` 映射为 `ToolResult`。Child lineage、中断、checkpoint 和 resume 由 Workflow 负责管理。`Adapter.Input` 必须确定且可安全重放；外部副作用应放在 child Agent 中。

## 示例

[example_test.go](./example_test.go) 把一个 child Agent 适配为 tool，并执行一次委派任务。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- 确定地完成协议映射，不引入第二套 runtime 或事件流。
- Child 执行保留正常的 Workflow lineage 与 resume 行为。

## 限制

- Adapter 输入必须确定且可安全重放。
- Adapter 不提供监督、规划或业务副作用能力。

## 何时选择其他 Agent

当模型不需要把 child 视作 tool 时，直接使用 Workflow child composition。
