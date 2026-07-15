# OpenAI Codex model provider

Chinese documentation: [README_zh.md](README_zh.md)

`codex` implements `gopact.Model` and `gopact.StreamingModel` for the OpenAI Codex backend available to eligible ChatGPT plans. It uses OAuth credentials produced by [`codexauth`](../codexauth), sends Responses API requests, and consumes the required SSE stream for both `Invoke` and `InvokeStream`.

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

model, err := codex.New("gpt-5.4", source)
if err != nil {
	return err
}

response, err := model.Invoke(ctx, model.NewRequest(
	gopact.UserMessage("Explain the failing test."),
))
```

Use a model slug enabled for the signed-in account; plan availability can differ. Do not point the store at `~/.codex/auth.json`: this package intentionally does not coordinate credential refreshes with Codex CLI.

For a one-off call with credentials already held in memory, `StaticTokenSource` is available, but it cannot refresh an expired token:

```go
model, err := codex.New("gpt-5.4", codex.StaticTokenSource(tokens))
```

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

A custom model-tool loop must append the complete `ModelResponse.Message` to history unchanged, then append one `tool` message for each `ToolCallIntent.Calls` entry in the same order. Response-state parts are opaque and must never be rendered as user text.

Image/audio input, hosted Codex tools, sampling controls (`temperature`, `top_p`, `stop`, and `seed`), and gopact output protocols are not implemented; requests using them fail explicitly.

## Security and protocol boundary

- OAuth credentials are obtained from the `TokenSource` for each call and are never persisted by the model package.
- Cross-origin redirects are rejected before credentials can be forwarded.
- Error bodies and SSE failures are bounded, and known OAuth token values are redacted from returned backend errors.
- A refreshing source gets one forced rotation after HTTP 401. Transient transport, HTTP 429, and HTTP 5xx failures are retried only before stream consumption.
- Request, SSE frame, accumulated output, and total stream sizes are bounded.

The default URL, auth headers, request shape, and SSE lifecycle track the OpenAI Codex source at commit [`1bbdb327`](https://github.com/openai/codex/tree/1bbdb32789e1f79932df44941236ea3658f6e965): [provider URL](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/model-provider-info/src/lib.rs), [bearer/account headers](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/model-provider/src/bearer_auth_provider.rs), [Responses request](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/codex-api/src/common.rs), and [SSE events](https://github.com/openai/codex/blob/1bbdb32789e1f79932df44941236ea3658f6e965/codex-rs/codex-api/src/sse/responses.rs).
