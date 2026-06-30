# gopact-ext

Official extensions for `github.com/gopact-ai/gopact`.

This repository uses one Git repo with separate Go modules per extension, so users can depend on only what they need.

## Modules

- `agents/planexec`: Plan-Execute agent template.
- `agents/react`: ReAct-style model/tool loop agent template.
- `models/agnes`: Agnes AI OpenAI-compatible text model provider adapter.
- `models/ark`: Volcengine Ark Chat Completions provider adapter.
- `models/openai`: OpenAI-shaped Chat Completions and Responses provider adapter.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.0
go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.0
go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.2
go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.0
go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.0
```

Extension modules are versioned with Go submodule tags such as `models/openai/v0.5.0`.

## Development

```bash
git diff --check
go test -count=1 ./models/openai/...
go test -count=1 ./models/ark/...
go test -count=1 ./models/agnes/...
go vet ./models/openai/...
go vet ./models/ark/...
go vet ./models/agnes/...
```

## Integration Tests

Provider modules include opt-in real-service tests behind the `integration` build tag:

```bash
GOWORK=off go test -tags=integration -count=1 ./models/openai/...
GOWORK=off go test -tags=integration -count=1 ./models/ark/...
GOWORK=off go test -tags=integration -count=1 ./models/agnes/...
go test -tags=integration -count=1 ./tests/agents/...
```

The tests load `.env` from the repo root when present. Keep `.env` local and set provider credentials with `GOPACT_OPENAI_API_KEY`, `GOPACT_ARK_API_KEY` or `GOPACT_LLM_TOKEN`, and `GOPACT_AGNES_API_KEY`.
