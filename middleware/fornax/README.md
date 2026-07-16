# Fornax middleware

Chinese documentation: [README_zh.md](README_zh.md)

`fornax` wraps a `gopact` Agent and reports its Agent, Workflow, and node spans to a Fornax OTLP/HTTP trace endpoint.

Configuration is explicit. The middleware does not load `SpaceID`, `Endpoint`, or `Authorization` from environment variables; applications decide how to obtain and manage them.

```go
middleware, err := fornax.New(ctx, fornax.Config{
	SpaceID:       spaceID,
	Endpoint:      endpoint,
	Authorization: authorization,
})
if err != nil {
	return err
}
defer middleware.Close(context.Background())

tracedAgent := middleware.Use(target)
response, err := tracedAgent.Invoke(ctx, request)
```

`Use` preserves `InvokeStream` when the target's dynamic type implements `agent.StreamingAgent`; use `UseStreaming` when the target is statically typed as `agent.StreamingAgent`. Streaming is traced through completion, failure, or consumer cancellation.

`Endpoint` is the complete Fornax OTLP/HTTP trace URL. `Authorization` is sent unchanged as the HTTP `Authorization` header, and `SpaceID` is sent as `cozeloop-workspace-id`.

The Agent invocation is reported as `fornax_query`, with the Workflow RunID and SessionID mapped to Fornax `message_id` and `thread_id`. Nested Workflow runs are reported as `agent`; nodes named `model` and `tool` use their corresponding Fornax span types, and other nodes use `graph`. Existing event sinks passed to `Invoke` remain attached. Call `Close` during application shutdown to flush pending spans.

Trace input and output follow Fornax's 4 MB field limit. Oversized values are omitted and listed in `cut_off`; streaming chunks are still forwarded to the application without truncation.

The core Workflow event contract contains lifecycle metadata rather than provider request bodies, token usage, or model/tool results. Consequently, this middleware reports node timing and status but cannot synthesize those provider-specific fields.
