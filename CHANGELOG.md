# Changelog

## 2026-07-01

- `agents/agenttool/v0.1.1`: require `github.com/gopact-ai/gopact v0.0.7`.
- `agents/planexec/v0.2.1`: publish approval, checkpoint, and model request option support on top of `gopact v0.0.7`.
- `agents/react/v0.2.1`: publish checkpoint and model request option support on top of `gopact v0.0.7`.
- `devagent/filesnapshot/v0.1.0`: add a file snapshot scanner that returns `gopacttest.FileSnapshot` evidence inputs.
- `devagent/gitdiff/v0.1.0`: add a git diff scanner that returns `gopacttest.DiffSnapshot` evidence inputs.
- `models/openai/v0.5.3`: require `gopact v0.0.7` and publish full-feature request coverage for chat completions and responses.
- `models/ark/v0.2.1`: require `gopact v0.0.7` and publish provider integration test coverage.
- `models/agnes/v0.1.1`: require `gopact v0.0.7`, depend on `models/openai/v0.5.3`, and publish provider integration test coverage.
- Update cross-template agent tests to use released extension module versions without local `replace` directives.

## models/openai/v0.5.0 - 2026-06-30

- Require `github.com/gopact-ai/gopact v0.0.3` and align provider calls with `gopact.ModelRequestOption`.
- Remove OpenAI request-option aliases such as `openai.WithTemperature`; use `gopact.NewModelRequest` and `gopact.WithTemperature` for per-call parameters.
- Keep OpenAI-specific options focused on API/client behavior such as `WithResponsesAPI`, `WithChatCompletionsAPI`, and `WithHTTPClient`.
- Change `Generate` and `Stream` to accept a complete `gopact.ModelRequest` without trailing request options.

## models/ark/v0.2.0 - 2026-06-30

- Require `github.com/gopact-ai/gopact v0.0.3` and update the provider contract to accept complete `gopact.ModelRequest` values.

## models/openai/v0.4.0 - 2026-06-30

- Require `github.com/gopact-ai/gopact v0.0.2` and use core `gopact.ModelOption` for client defaults and per-call overrides.
- Remove the old `Options`/`New`/`WithModels`/`ProviderModel` initialization path.
- Split model selection from capabilities: use `WithModel(...)` plus `EnableStreaming()`, `EnableToolCalling()`, and related helpers.
- Let `Generate` and `Stream` use the configured default model when `ModelRequest.Model` is empty.

## models/openai/v0.3.2 - 2026-06-30

- Add `WithModel` so model metadata can inherit the client provider without repeating `ProviderModel(...)` at call sites.

## models/openai/v0.3.1 - 2026-06-30

- Add `NewClient` and feature options for API mode, model parameters, thinking, reasoning, HTTP clients, and model metadata.
- Add provider/model/capability helpers so examples do not hard-code provider capability structs.

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
