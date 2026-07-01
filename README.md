# gopact-ext

Official extensions for `github.com/gopact-ai/gopact`.

This repository uses one Git repo with separate Go modules per extension, so users can depend on only what they need.

See [FEATURES.md](./FEATURES.md) for the executable capability coverage matrix.

## Modules

- `agents/agenttool`: A2A agent-as-tool adapter.
- `agents/planexec`: Plan-Execute agent template.
- `agents/react`: ReAct-style model/tool loop agent template.
- `devagent/filesnapshot`: File snapshot scanner for Dev Agent evidence collection.
- `devagent/gitdiff`: Git diff scanner for Dev Agent evidence collection.
- `models/agnes`: Agnes AI OpenAI-compatible text model provider adapter.
- `models/ark`: Volcengine Ark Chat Completions provider adapter.
- `models/openai`: OpenAI-shaped Chat Completions and Responses provider adapter.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.8
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.8
go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.8
go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.7
go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.7
go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.10
go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.8
go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.8
```

Extension modules are versioned with Go submodule tags such as `models/openai/v0.5.0`.

## Development

```bash
git diff --check
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go mod tidy); done
git diff --exit-code
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -race -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go vet ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && golangci-lint run ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -coverprofile=coverage.out ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && govulncheck ./...); done
```

## Integration Tests

Provider modules include opt-in real-service tests behind the `integration` build tag:

```bash
cp .env.example .env
(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/agnes && go test -tags=integration -count=1 ./...)
(cd tests/agents && go test -tags=integration -count=1 ./...)
```

The tests load `.env` from the repo root when present. Keep `.env` local. Agnes and Ark accept the shared `GOPACT_LLM_BASEURL`, `GOPACT_LLM_TOKEN`, and `GOPACT_LLM_MODEL` keys; provider-specific keys such as `GOPACT_AGNES_API_KEY`, `GOPACT_ARK_API_KEY`, and `GOPACT_OPENAI_API_KEY` still work.
