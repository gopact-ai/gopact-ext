# ByteDance Fornax middleware

Chinese documentation: [README_zh.md](README_zh.md)

`fornax` is a ByteDance-specific middleware that wraps a `gopact` Agent and reports its Agent, Workflow, and node spans to a Fornax trace ingest endpoint.

Configuration is explicit. The middleware does not load credentials from environment variables; applications decide how to obtain and manage them.

## Basic usage

```go
middleware, err := fornax.New(ctx, fornax.Config{
	AK: ak,
	SK: sk,
})
if err != nil {
	return err
}
defer middleware.Close(context.Background())

tracedAgent := middleware.Use(target)
response, err := tracedAgent.Invoke(ctx, request)
```

By default, spans contain operational metadata only. Agent messages, model requests and responses, tool arguments, result previews, streaming output, and verbose error details are not reported.

## Full configuration

```go
middleware, err := fornax.New(ctx, fornax.Config{
	AK:      ak,
	SK:      sk,
	SpaceID: "12345", // optional; verifies the authenticated workspace
	Region: "CN", // optional; use SG, US, Asia-SouthEastBD, or I18N-DEV as needed
	Endpoint: "https://fornax.bytedance.net/open-api/observability/traces/ingest", // optional override
	PSM:      "your.service.psm", // optional; defaults to unknown_psm
	UserID:   "default-user",
	DeviceID: "default-device",
	CaptureContent: false, // safe default; see Content capture before enabling
	Metadata: map[string]string{
		"tenant": "tenant-1",
	},
})
if err != nil {
	return err
}
defer middleware.Close(context.Background())

tracedAgent := middleware.Use(target)
response, err := tracedAgent.Invoke(ctx, request)
```

## Content capture

Set `CaptureContent: true` only when the application has explicitly approved exporting request and response content to Fornax. This enables `cozeloop.input` and `cozeloop.output` on root, Agent, model, and tool spans, including messages, tool schemas and arguments, result previews, and aggregated streaming output. It also enables raw error attributes because provider errors can contain response payloads. The zero value is `false`; there is no per-request override, so an application cannot accidentally enable capture by propagating the wrong context.

Metadata remains available when content capture is disabled: span hierarchy, run/session/node identifiers, model and tool names, tool call IDs, token usage, finish reason, error status, latency, and application-provided tags. The original error is still returned to the application.

## Per-request tags

`UserID`, `DeviceID`, and `Metadata` in `Config` are defaults. Use context helpers when these values differ per request:

```go
ctx = fornax.WithUserID(ctx, "user-456")
ctx = fornax.WithDeviceID(ctx, "device-456")
ctx = fornax.WithMetadata(ctx, map[string]string{"request_id": "req-1"})
response, err := tracedAgent.Invoke(ctx, request)
```

`Use` preserves `InvokeStream` when the target's dynamic type implements `agent.StreamingAgent`; use `UseStreaming` when the target is statically typed as `agent.StreamingAgent`. Streaming is traced through completion, failure, or consumer cancellation.

`AK` and `SK` are the Fornax space credentials. `Region` is optional and is passed explicitly to the Fornax authentication and trace endpoints instead of relying on `FORNAX_CUSTOM_REGION`. `SpaceID` is optional; when provided, it must match the workspace resolved from AK/SK. `Endpoint` is an advanced override for the complete Fornax trace ingest URL. `PSM` is sent in the Fornax authentication body and exported as span `service_name` plus the `psm` tag; when omitted it defaults to `unknown_psm`, matching the Fornax SDK fallback. `UserID`, `DeviceID`, and `Metadata` are exported as string tags on every reported span, and can be overridden per request with `WithUserID`, `WithDeviceID`, and `WithMetadata`.

The Agent invocation is reported as `fornax_query`, with an `Agent` span under it. Workflow RunID and SessionID are mapped to Fornax `message_id` and `thread_id`. Nested Workflow runs are reported as `Agent`; nodes named `model` and `tool` use their corresponding Fornax span types, and other nodes use `graph`. Existing event sinks passed to `Invoke` remain attached. Call `Close` during application shutdown to flush pending spans.

## ID correspondence

| Source value | Exported value | Meaning in Fornax |
| --- | --- | --- |
| AK/SK authenticated workspace | trace ingest `workspace_id` | Target workspace; it is not a trace or span ID. |
| Root Workflow `RunID` | `messaging.message.id` and `gopact.run_id` | Fornax `message_id` and the gopact run identifier. |
| Nested Workflow `RunID` | `gopact.run_id` | Child Agent run identifier; it does not replace the root `message_id`. |
| Workflow `SessionID` | `session.id` on the root span | Fornax `thread_id`, used to group related messages. |
| `Config.PSM` | Authentication `psm`, span `service_name`, and span tag `psm` | Reporting service identity; defaults to `unknown_psm`. |
| `Config.UserID` / `Config.DeviceID`, or context `WithUserID` / `WithDeviceID` | Span tags `user_id` / `device_id` | End-user dimensions; context values override Config defaults for one invocation. |
| `Config.Metadata`, or context `WithMetadata` | Span string tags | Custom searchable metadata; context tags add to Config defaults, and reserved trace protocol keys are ignored. |
| Workflow `ParentRunID` | OTel parent relationship and `gopact.parent_run_id` | Links a nested Agent span to its parent run. |
| Workflow `DefinitionID` | `agent_name`; nested Agent span name | Identifies the Workflow/Agent definition. |
| Node `NodeID` | Node span name and `gopact.node_id` | `model` and `tool` select those span types; other values use `graph`. |
| Node `ActivationID` / `AttemptID` | `gopact.activation_id` / `gopact.attempt_id` | Identifies a node activation and a particular attempt. |
| `ToolCall.ID` | `tool_call_id` on a typed tool span | Identifies the model-requested tool call; it is not an OTel Span ID. |
| OTel Trace ID / Span ID | Native OTLP IDs | Inherited from the input context or generated by OTel; never derived from a RunID or SessionID. |

When content capture is enabled, trace input and output follow Fornax's 4 MB field limit. Oversized values are omitted and listed in `cut_off`; streaming chunks are still forwarded to the application without truncation. When it is disabled, the middleware does not aggregate streaming content for tracing.

The core Workflow event contract contains lifecycle metadata rather than provider request bodies, token usage, or model/tool results. Component-aware Agents can additionally emit live typed model/tool observations. The middleware always uses their operational metadata to enrich the same node span; when content capture is enabled, it also reports their request, response, and tool outcome payloads. Fields that are not emitted by the model/tool adapter are not synthesized.
