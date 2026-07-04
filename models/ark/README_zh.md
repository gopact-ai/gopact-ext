# ark

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`models/ark` 是 Volcengine Ark SDK provider adapter。它通过 `github.com/volcengine/volcengine-go-sdk/service/arkruntime` 接入 Ark，支持 Ark API key，也支持 AK/SK。

注意：如果你只是把 Ark endpoint 当作 OpenAI-compatible 服务测试，应使用 `models/openai`，并通过 `GOPACT_LLM_TOKEN` 传 token。`models/ark` 的目标是 Ark SDK 路径。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.28
```

## API key 用法

```go
client, err := ark.New(ark.Options{
	BaseURL: os.Getenv("GOPACT_LLM_BASEURL"),
	Region:  ark.DefaultRegion,
	APIKey:  os.Getenv("GOPACT_ARK_API_KEY"),
})
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithModel(os.Getenv("GOPACT_LLM_MODEL")),
	gopact.WithMessages(gopact.UserMessage("Reply with one sentence.")),
	gopact.WithMaxOutputTokens(512),
	gopact.WithTemperature(0.2),
))
```

## AK/SK 用法

```go
client, err := ark.New(ark.Options{
	BaseURL:   "https://ark.cn-beijing.volces.com",
	Region:    ark.DefaultRegion,
	AccessKey: accessKey,
	SecretKey: secretKey,
})
```

`BaseURL` 默认为 `https://ark.cn-beijing.volces.com/api/v3`。如果传入的地址没有 `/api/v3`，模块会自动补齐。

## 能力

- Chat Completions `Generate`。
- SDK streaming，并转换成 `gopact.EventModelMessage`。
- model、message、tools、tool choice、max output tokens、temperature、top-p、thinking、reasoning effort、structured output 参数映射。
- API key 与 AK/SK 两种鉴权方式。
- timeout/cancel 和 provider error classification。

## 验证

```bash
(cd models/ark && go test -count=1 ./...)
(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)
```
