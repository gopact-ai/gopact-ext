# GLM Provider

[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/glm.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/glm)

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

`models/glm` 将 GLM/智谱 AI 的 OpenAI-compatible Chat Completions API 适配到 `gopact` 模型契约。它基于 `models/openai`，提供国内开放平台和国际 Z.AI Coding Plan 两套 endpoint 默认值。

安装：

```bash
go get github.com/gopact-ai/gopact-ext/models/glm@v0.1.1
```

## Endpoint

- 国内站：`glm.New` 或 `glm.NewClient`，默认使用 `glm.DefaultBaseURL`。
- 国际站：`glm.NewInternational` 或 `glm.NewInternationalClient`，默认使用 `glm.DefaultInternationalBaseURL`。

两套 profile 都使用 OpenAI-compatible Chat Completions。业务代码应从配置传入 model，不要把部署相关 model ID 写死在代码里。

## 用法

```go
client, err := glm.NewInternational(
	os.Getenv("GOPACT_GLM_INTERNATIONAL_API_KEY"),
	gopact.WithModel(os.Getenv("GOPACT_GLM_MODEL")),
	gopact.EnableStreaming(),
	glm.DisableThinking(),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.UserMessage("Reply with one sentence.")),
	gopact.WithMaxOutputTokens(512),
	gopact.WithTemperature(0.2),
))
```

如果需要 mock server 或自定义网关，用 `NewClient` / `NewInternationalClient`。

## 真实 provider 测试

真实测试需要显式启用 `integration` build tag：

```bash
(cd models/glm && go test -tags=integration -count=1 ./...)
```

环境变量：

- `GOPACT_GLM_API_KEY`：国内站 API key。
- `GLM_API_KEY`：通用 GLM API key alias，适合复用仓库 secret。
- `GOPACT_GLM_BASEURL`：可选国内站 endpoint override。
- `GOPACT_GLM_INTERNATIONAL_API_KEY`：国际站 API key。
- `GOPACT_GLM_INTERNATIONAL_BASEURL`：可选国际站 endpoint override。
- `GOPACT_GLM_MODEL`：调用方配置的 model。

缺少对应 API key 或 model 时，integration 测试会 skip。
