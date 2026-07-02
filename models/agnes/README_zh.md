# agnes

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`models/agnes` 是 Agnes AI provider adapter。Agnes 暴露 OpenAI-compatible Chat Completions API，本模块在 `models/openai` 之上提供 Agnes 默认 base URL、默认 model 和 thinking toggle。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.16
```

## 用法

```go
client, err := agnes.New(
	os.Getenv("GOPACT_AGNES_API_KEY"),
	agnes.EnableThinking(),
	gopact.WithModel(os.Getenv("GOPACT_LLM_MODEL")),
	gopact.WithMaxOutputTokens(1024),
	gopact.EnableStreaming(),
	gopact.EnableToolCalling(),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.UserMessage("Reply with one sentence.")),
	gopact.WithTemperature(0.2),
))
```

如果需要自定义网关或 mock server，可以使用 `NewClient`：

```go
client, err := agnes.NewClient(
	os.Getenv("GOPACT_LLM_BASEURL"),
	os.Getenv("GOPACT_LLM_TOKEN"),
	agnes.DisableThinking(),
)
```

默认值：

- `DefaultBaseURL`: `https://apihub.agnes-ai.com/v1`
- `DefaultModel`: `agnes-2.0-flash`
- thinking toggle 通过 OpenAI-compatible Chat Completions 的 `chat_template_kwargs.enable_thinking` 发送。

## 能力

- Agnes provider streaming。
- Agnes provider tool calling。
- Agnes provider structured output。
- Agnes provider thinking toggle。
- Agnes provider error classification。
- Agnes provider cancel and timeout。
- 与 ReAct、Plan-Execute、Agent-as-Tool 的 Agnes-backed agent templates integration coverage。

## 验证

```bash
(cd models/agnes && go test -count=1 ./...)
(cd models/agnes && go test -tags=integration -count=1 ./...)
```
