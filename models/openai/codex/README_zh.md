# OpenAI Codex 模型 Provider

[English documentation](README.md)

`codex` 为可使用 Codex 的 ChatGPT plan 实现 `gopact.Model`、`gopact.StreamingModel` 与 `gopact.ModelCatalog`。它使用 [`codexauth`](../codexauth) 产生的 OAuth 凭据发送 Responses API 请求；`Invoke` 和 `InvokeStream` 都会消费 backend 要求的 SSE stream。

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

model, err := codex.New("", source)
if err != nil {
	return err
}

catalog, err := model.ListModels(ctx)
if err != nil {
	return err
}
selectedModel := catalog.Models[0].ID // 实际应用应让用户选择

request := model.NewRequest(
	gopact.UserMessage("解释这个失败的测试。"),
)
request.Model = selectedModel
response, err := model.Invoke(ctx, request)
```

构造器中的空模型是有意设计：应用可以先完成认证并发现当前账号的模型目录，再让用户选择。真正发起生成时仍必须设置 `request.Model`。不同 plan 的可用范围可能不同。不要让 store 指向 `~/.codex/auth.json`：本包有意不与 Codex CLI 协调凭据刷新。

如果只是对已在内存中的 token 做一次短调用，可以使用 `StaticTokenSource`，但它无法刷新过期 token：

```go
model, err := codex.New("gpt-5.4", codex.StaticTokenSource(tokens))
```

## 账号模型与订阅用量

`ListModels` 返回当前登录账号公开的模型 slug、展示名称、描述、reasoning level、模态、上下文窗口、feature flag、优先级与 catalog ETag。

`SubscriptionUsage` 返回 ChatGPT plan 类型、主/次用量窗口、credits、workspace 消费控制与单独计量的功能限制：

```go
usage, err := model.SubscriptionUsage(ctx)
if err != nil {
	return err
}
fmt.Printf("plan=%s\n", usage.Plan)
if usage.RateLimit != nil && usage.RateLimit.Primary != nil {
	fmt.Printf("used=%d%%\n", usage.RateLimit.Primary.UsedPercent)
}
```

不同 plan 和 workspace 暴露的控制项不同，调用方应把可选嵌套字段视为 nullable。这里返回的是 ChatGPT 订阅信息，不是 OpenAI API 平台的组织用量或成本；后者必须使用独立的 [`openai.AdminClient`](../README_zh.md)。

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

自定义 model-tool loop 必须把完整的 `ModelResponse.Message` 原样追加到 history，执行其中的每个 `ModelResponse.Message.ToolCalls`，再为每个调用追加一条 `ToolCallID` 与原调用一致的 `tool` message。关联已经由 ID 明确表达，因此 tool output 可以按任意顺序追加。response-state part 是不透明状态，禁止作为用户文本渲染。

当前未实现 image/audio 输入、Codex 托管 tool、采样控制（`temperature`、`top_p`、`stop`、`seed`）以及 gopact output protocol；使用这些能力的请求会显式失败。

## 安全与协议边界

- 模型包每次调用都从 `TokenSource` 获取 OAuth 凭据，本身不持久化 token。
- 跨 origin redirect 会在凭据可能被转发前拒绝。
- 错误 body 与 SSE failure 都有大小上限；返回 backend 错误前会脱敏已知 OAuth token。
- HTTP 401 时，支持刷新的 source 会被强制刷新一次；transport、HTTP 429 和 HTTP 5xx 只会在开始消费 stream 前重试。
- request、SSE frame、累计输出和整个 stream 都有大小边界。

默认 URL、认证 header、请求结构、SSE 生命周期、模型发现与订阅用量按 OpenAI Codex commit [`c28770a4`](https://github.com/openai/codex/tree/c28770a42f9ed7bff549d7283d14172b8b061eaa) 对齐：[provider URL](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/model-provider-info/src/lib.rs)、[bearer/account header](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/model-provider/src/bearer_auth_provider.rs)、[Responses request](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/codex-api/src/common.rs)、[SSE event](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/codex-api/src/sse/responses.rs)、[模型 endpoint](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/codex-api/src/endpoint/models.rs) 与 [用量 endpoint](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/backend-client/src/client/rate_limit_resets.rs)。
