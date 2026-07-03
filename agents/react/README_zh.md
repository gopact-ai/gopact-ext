# react

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`react` 是 ReAct 风格的 model/tool loop agent template。它适合“模型选择工具、执行工具、根据结果继续推理或完成回答”的交互式任务，并保持 provider-neutral。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.26
```

## 用法

```go
uppercase := gopact.ToolFunc{
	SpecValue: gopact.ObjectToolSpec(
		"uppercase",
		"Uppercase text.",
		gopact.RequiredStringField("text", "Text to uppercase."),
	),
	InvokeFunc: func(ctx context.Context, raw json.RawMessage) (gopact.ToolResult, error) {
		return gopact.TextToolResult("GOPACT"), nil
	},
}

agent, err := react.NewModelAgent(
	model,
	react.WithTools(ctx, uppercase),
	react.WithMaxIterations(4),
	react.WithModelOptions(
		gopact.WithMaxOutputTokens(1024),
		gopact.WithTemperature(0.2),
	),
)
if err != nil {
	return err
}

events, err := gopacttest.CollectEvents(agent.Run(ctx, "uppercase gopact"))
```

## 能力边界

- `NewModelAgent` 接受 `gopact.ResponseModel`，如果 provider 支持 streaming，会通过 core adapter 使用 streaming。
- `WithTools` 注册 visible local tools；tool policy、middleware 和 registry 仍由 core `tools` 包提供。
- `WithMemory` 支持同步记忆写入，也支持 deferred memory effects，便于宿主进程异步处理。
- checkpoint、artifact verifier、final verifier 都是可插拔能力，模板不内置持久化后端。

## 验证

```bash
(cd agents/react && go test -count=1 ./...)
```
