# OpenAI Codex model provider

Chinese documentation: [README_zh.md](README_zh.md)

`codex` implements `gopact.Model`, `gopact.StreamingModel`, and `gopact.ModelCatalog` for the OpenAI Codex backend available to eligible ChatGPT plans. It uses OAuth credentials produced by [`codexauth`](../codexauth), sends Responses API requests, and consumes the required SSE stream for both `Invoke` and `InvokeStream`.

This is distinct from [`models/openai`](../), which uses an API key and a public OpenAI-compatible endpoint. The ChatGPT Codex backend is an implementation-level service used by the official Codex client, not a generic compatibility promise. Its protocol can change upstream.

## End-to-end setup

First complete device login and persist the entire token value in an operating-system keychain or an equivalently protected store:

```go
auth, err := codexauth.New()
if err != nil {
	return err
}

device, err := auth.Start(ctx)
if err != nil {
	return err
}
fmt.Printf("Open %s and enter %s\n", device.VerificationURL, device.UserCode)

tokens, err := auth.Wait(ctx, device)
if err != nil {
	return err
}
if err := secretStore.Save(ctx, tokens); err != nil {
	return err
}
```

The store must implement `codexauth.Store`. `Source` loads current credentials for every model call, refreshes shortly before expiry, and atomically persists rotated refresh tokens. A shared multi-process store must also coordinate refreshes across processes.

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
selectedModel := catalog.Models[0].ID // let the user choose in a real application

request := model.NewRequest(
	gopact.UserMessage("Explain the failing test."),
)
request.Model = selectedModel
response, err := model.Invoke(ctx, request)
```

The empty constructor model is intentional: it lets an application authenticate and discover the signed-in account's model catalog before the user chooses a model. A generation request still requires `request.Model`. Plan availability can differ. Do not point the store at `~/.codex/auth.json`: this package intentionally does not coordinate credential refreshes with Codex CLI.

For a one-off call with credentials already held in memory, `StaticTokenSource` is available, but it cannot refresh an expired token:

```go
model, err := codex.New("gpt-5.4", codex.StaticTokenSource(tokens))
```

## Account models and subscription usage

`ListModels` returns model slugs, display names, descriptions, reasoning levels, modalities, context windows, feature flags, priority, and the catalog ETag advertised for the signed-in account.

`SubscriptionUsage` returns the ChatGPT plan type, primary and secondary usage windows, credits, workspace spending control, and separately metered feature limits:

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

Treat optional nested fields as nullable because plans and workspaces expose different controls. This is ChatGPT subscription information, not OpenAI API-platform organization usage or cost data. The latter requires the separate [`openai.AdminClient`](../README.md).

## Streaming

`InvokeStream` yields only visible assistant text. Reasoning and tool-call deltas are available through model event sinks.

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

The stream must end with `response.completed`; a clean connection close without that event is reported as an incomplete response.

## Tools and continuation state

Function tools, named/required tool choice, structured JSON output, reasoning effort, maximum output tokens, usage, and model events are normalized into the provider-neutral gopact protocol.

Codex reasoning and function-call items must be replayed on the next model turn. The provider stores them as `MessagePartTypeResponseItem` parts on the assistant message. Official gopact Agents preserve those parts automatically, so the normal ReAct composition works directly:

```go
target, err := react.New(
	agent.Identity{Name: "coder", Description: "uses repository tools", Version: "v1"},
	model,
	react.WithTools(tools...),
)
```

A custom model-tool loop must append the complete `ModelResponse.Message` to history unchanged, execute each `ModelResponse.Message.ToolCalls` entry, then append one `tool` message whose `ToolCallID` matches the originating call. Tool outputs may be appended in any order because association is explicit. Response-state parts are opaque and must never be rendered as user text.

Image/audio input, hosted Codex tools, sampling controls (`temperature`, `top_p`, `stop`, and `seed`), and gopact output protocols are not implemented; requests using them fail explicitly.

## Security and protocol boundary

- OAuth credentials are obtained from the `TokenSource` for each call and are never persisted by the model package.
- Cross-origin redirects are rejected before credentials can be forwarded.
- Error bodies and SSE failures are bounded, and known OAuth token values are redacted from returned backend errors.
- A refreshing source gets one forced rotation after HTTP 401. Transient transport, HTTP 429, and HTTP 5xx failures are retried only before stream consumption.
- Request, SSE frame, accumulated output, and total stream sizes are bounded.

The default URL, auth headers, request shape, SSE lifecycle, model discovery, and subscription usage track the OpenAI Codex source at commit [`c28770a4`](https://github.com/openai/codex/tree/c28770a42f9ed7bff549d7283d14172b8b061eaa): [provider URL](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/model-provider-info/src/lib.rs), [bearer/account headers](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/model-provider/src/bearer_auth_provider.rs), [Responses request](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/codex-api/src/common.rs), [SSE events](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/codex-api/src/sse/responses.rs), [models endpoint](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/codex-api/src/endpoint/models.rs), and [usage endpoint](https://github.com/openai/codex/blob/c28770a42f9ed7bff549d7283d14172b8b061eaa/codex-rs/backend-client/src/client/rate_limit_resets.rs).
