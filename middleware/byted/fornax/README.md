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
	Region: "CN", // optional; also BOE, SG, BOEI18N, US, Asia-SouthEastBD, or I18N-DEV
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

Set `CaptureContent: true` only when the application has explicitly approved exporting request and response content to Fornax. When enabled, the exporter may populate the top-level `input` and `output` fields on root and Agent spans, and on model or tool spans when the corresponding component events are emitted. Payloads can include messages, tool schemas and arguments, result previews, and aggregated streaming output. Raw errors are exported as the `tags_string["error"]` value. The zero value is `false`, and there is no per-request override.

Non-empty, non-reserved keys deliberately placed in `Config.Metadata`, `agent.Request.Metadata`, or `WithMetadata` are exported as tags regardless of `CaptureContent`. Non-empty `UserID` and `DeviceID` values are also exported regardless of that setting.

Metadata remains available when content capture is disabled: span hierarchy, run/session/node identifiers, model and tool names, tool call IDs, token usage, finish reason, error status, latency, and application-provided tags. The original error is still returned to the application.

## Per-request tags

`UserID`, `DeviceID`, and `Metadata` in `Config` are defaults. `agent.Request.Metadata` is also exported as string tags for that invocation. Use the context helpers when identity or metadata differs per request:

```go
ctx = fornax.WithUserID(ctx, "user-456")
ctx = fornax.WithDeviceID(ctx, "device-456")
ctx = fornax.WithMetadata(ctx, map[string]string{"request_id": "req-1"})
response, err := tracedAgent.Invoke(ctx, request)
```

`Use` preserves `InvokeStream` when the target's dynamic type implements `agent.StreamingAgent`; use `UseStreaming` when the target is statically typed as `agent.StreamingAgent`. Streaming is traced through completion, failure, or consumer cancellation.

`AK` and `SK` are the Fornax space credentials. `Region` is optional and selects the authentication host and default trace ingest URL; supported values are `CN`, `BOE`, `SG`, `BOEI18N`, `US`, `Asia-SouthEastBD`, and `I18N-DEV`, while empty or unrecognized values use `CN`. `SpaceID` is optional; when provided, it must match the workspace resolved from AK/SK. `Endpoint` is an advanced override for the complete Fornax trace ingest URL; authentication still uses `Region`. `PSM` is sent in the Fornax authentication body and exported as span `service_name` plus the `psm` tag; when omitted it defaults to `unknown_psm`, matching the Fornax SDK fallback. `UserID`, `DeviceID`, and accepted `Metadata` entries are exported as string tags on every reported span, and can be overridden per request with `WithUserID`, `WithDeviceID`, and `WithMetadata`.

The Agent invocation is reported as `fornax_query`, with an `Agent` span under it. Invocation RunID and SessionID from `RunOptions` become Fornax `message_id` and `thread_id`; Workflow lifecycle events additionally attach `gopact.run_id` to each Workflow span. Nested Workflow runs are reported as `Agent`; nodes named `model` and `tool` use their corresponding Fornax span types, and other nodes use `graph`. Existing event sinks passed to `Invoke` remain attached. Call `Close` during application shutdown to flush pending spans.

## ID correspondence

| Source value | Exported value | Meaning in Fornax |
| --- | --- | --- |
| AK/SK authenticated workspace | trace ingest `workspace_id` | Target workspace; it is not a trace or span ID. |
| Invocation `RunID` from `RunOptions` | `tags_string["message_id"]` on the root query span | Fornax message ID. |
| Workflow `RunID` emitted by lifecycle events | `tags_string["gopact.run_id"]`; the root Workflow also writes `tags_string["message_id"]` | Identifies the root or child Workflow run; a child run never replaces the root message ID. |
| Workflow `SessionID` | `tags_string["thread_id"]` | Groups the reported spans for related messages. |
| `Config.PSM` | Authentication `psm`, span `service_name`, and span tag `psm` | Reporting service identity; defaults to `unknown_psm`. |
| `Config.UserID` / `Config.DeviceID`, or context `WithUserID` / `WithDeviceID` | Span tags `user_id` / `device_id` | End-user dimensions; context values override Config defaults for one invocation. |
| `Config.Metadata`, or context `WithMetadata` | Span string tags | Custom searchable metadata; context tags add to Config defaults, and reserved trace protocol keys are ignored. |
| Workflow `ParentRunID` | OTel parent relationship and `gopact.parent_run_id` | Links a nested Agent span to its parent run. |
| Workflow `DefinitionID` | `agent_name`; nested Agent span name | Identifies the Workflow/Agent definition. |
| Node `NodeID` | Node span name and `gopact.node_id` | `model` and `tool` select those span types; other values use `graph`. |
| Node `ActivationID` / `AttemptID` | `gopact.activation_id` / `gopact.attempt_id` | Identifies a node activation and a particular attempt. |
| `ToolCall.ID` | `tool_call_id` on a typed tool span | Identifies the model-requested tool call; it is not an OTel Span ID. |
| OTel trace, span, and parent IDs | Top-level `trace_id`, `span_id`, and `parent_id` | Inherited from the input context or generated by OTel; never derived from a RunID or SessionID. |

This module sends Fornax trace-ingest JSON, not OTLP. The internal `cozeloop.span_type`, `cozeloop.input`, `cozeloop.output`, and `cozeloop.status_code` attributes become the top-level `span_type`, `input`, `output`, and `status_code` fields. Other attributes are written to the corresponding `tags_string`, `tags_long`, `tags_double`, or `tags_bool` map.

When content capture is enabled, each encoded input or output field is limited to 4 MiB (4,194,304 bytes). Oversized non-streaming fields are omitted. For streaming output, aggregation stops before the next chunk would exceed the budget; an already aggregated prefix is exported only if its encoded response still fits. The root span records invocation-level truncation in `tags_string["cut_off"]`, while model and tool node spans record their own truncation. Every streaming chunk is still forwarded to the application in full. When capture is disabled, the middleware does not aggregate streaming content for tracing.

The core Workflow event contract contains lifecycle metadata rather than provider request bodies, token usage, or model and tool results. When a Workflow node is active, typed model or tool observations enrich that node span. Model observations emitted without an active node are reported in a dedicated model span; tool observations without an active node are ignored. Content payloads are exported only when `CaptureContent` is enabled, and fields not emitted by an adapter are not synthesized.
