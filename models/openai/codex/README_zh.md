# OpenAI Codex 模型 Provider

[English documentation](README.md)

`codex` 为可使用 Codex 的 ChatGPT plan 实现 `gopact.Model` 与 `gopact.StreamingModel`。它使用 [`codexauth`](../codexauth) 产生的 OAuth 凭据发送 Responses API 请求；`Invoke` 和 `InvokeStream` 都会消费 backend 要求的 SSE stream。

它与 [`models/openai`](../) 不同：后者使用 API key 和公开的 OpenAI-compatible endpoint。ChatGPT Codex backend 是官方 Codex client 使用的实现级服务，不是通用兼容性承诺，上游协议可能变化。

## 端到端接入

先完成设备码登录，并把完整 token 值保存到操作系统钥匙串或具备同等保护能力的 secret store：

```go
auth, err := codexauth.New()
if err != nil {
	return err
}

device, err := auth.Start(ctx)
if err != nil {
	return err
}
fmt.Printf("打开 %s 并输入 %s\n", device.VerificationURL, device.UserCode)

tokens, err := auth.Wait(ctx, device)
if err != nil {
	return err
}
if err := secretStore.Save(ctx, tokens); err != nil {
	return err
}
```

secret store 需要实现 `codexauth.Store`。`Source` 会在每次模型调用时加载当前凭据，在临近过期时刷新，并原子持久化轮换后的 refresh token。多进程共享同一 store 时，还必须在进程间协调 refresh。

```go
source, err := codexauth.NewSource(auth, secretStore)
if err != nil {
	return err
}

model, err := codex.New("gpt-5.4", source)
if err != nil {
	return err
}

response, err := model.Invoke(ctx, model.NewRequest(
	gopact.UserMessage("解释这个失败的测试。"),
))
```

模型 slug 必须在当前登录账号下可用，不同 plan 的可用范围可能不同。不要让 store 指向 `~/.codex/auth.json`：本包有意不与 Codex CLI 协调凭据刷新。

如果只是对已在内存中的 token 做一次短调用，可以使用 `StaticTokenSource`，但它无法刷新过期 token：

```go
model, err := codex.New("gpt-5.4", codex.StaticTokenSource(tokens))
```

## 流式输出

`InvokeStream` 只产出用户可见的 assistant 文本。reasoning 与 tool-call delta 通过 model event sink 交付。

```go
for chunk, err := range model.InvokeStream(ctx, request,
	gopact.WithModelEventHandler(handleModelEvent),
) {
	if err != nil {
		return err
	}
	fmt.Print(chunk.Text)
}
```

stream 必须以 `response.completed` 结束；连接正常关闭但缺少该事件时，会返回响应不完整错误。

## Tool 与续轮状态

function tool、named/required tool choice、结构化 JSON 输出、reasoning effort、最大输出 token、usage 和 model event 都会归一化到 provider-neutral 的 gopact 协议。

下一轮模型调用必须回放 Codex 的 reasoning 和 function-call item。provider 会把它们保存为 assistant message 上的 `MessagePartTypeResponseItem`。官方 gopact Agent 会自动保留这些 part，因此标准 ReAct 组合可以直接工作：

```go
target, err := react.New(
	agent.Identity{Name: "coder", Description: "uses repository tools", Version: "v1"},
	model,
	react.WithTools(tools...),
)
```

自定义 model-tool loop 必须把完整的 `ModelResponse.Message` 原样追加到 history，再按照 `ToolCallIntent.Calls` 的顺序逐个追加 `tool` message。response-state part 是不透明状态，禁止作为用户文本渲染。

当前未实现 image/audio 输入、Codex 托管 tool、采样控制（`temperature`、`top_p`、`stop`、`seed`）以及 gopact output protocol；使用这些能力的请求会显式失败。

## 安全与协议边界

- 模型包每次调用都从 `TokenSource` 获取 OAuth 凭据，本身不持久化 token。
- 跨 origin redirect 会在凭据可能被转发前拒绝。
- 错误 body 与 SSE failure 都有大小上限；返回 backend 错误前会脱敏已知 OAuth token。
- HTTP 401 时，支持刷新的 source 会被强制刷新一次；transport、HTTP 429 和 HTTP 5xx 只会在开始消费 stream 前重试。
- request、SSE frame、累计输出和整个 stream 都有大小边界。

默认 URL、认证 header、请求结构和 SSE 生命周期按 OpenAI Codex commit [`1bbdb327`](https://github.com/openai/codex/tree/1bbdb32789e1f79932df44941236ea3658f6e965) 对齐：[provider URL](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/model-provider-info/src/lib.rs)、[bearer/account header](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/model-provider/src/bearer_auth_provider.rs)、[Responses request](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/codex-api/src/common.rs) 和 [SSE event](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/codex-api/src/sse/responses.rs)。
