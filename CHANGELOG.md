# Changelog

## models/openai/v0.3.0 - 2026-06-29

- Add true SSE streaming for Chat Completions and Responses.
- Add model parameter options for max output tokens, temperature, top-p, thinking, and reasoning effort.
- Add streamed tool call argument aggregation.
- Map Responses reasoning summaries to `gopact.ContentPartReasoning`.

## models/openai/v0.2.1 - 2026-06-29

- Send Responses API message content as `input_text` and `input_image` parts.

## models/openai/v0.2.0 - 2026-06-29

- Add Responses API mode via `openai.APIResponses`.
- Keep Chat Completions as the default API mode.

## models/openai/v0.1.0 - 2026-06-29

- Add OpenAI-shaped Chat Completions provider adapter.
- Support host-owned provider name, base URL, API key, HTTP client, and model metadata.
- Support message conversion, tool definitions, assistant tool call round-tripping, usage metadata, and provider error classification.
- Add provider conformance tests and GitHub Actions CI for extension modules.
