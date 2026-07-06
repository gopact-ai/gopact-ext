# gopact-ext

#### Official providers, agent templates, and development-agent helpers for gopact.

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/openai.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/openai)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`gopact-ext` is the official extension repository for [`gopact`](https://github.com/gopact-ai/gopact). It uses one Git repository with independent Go submodules so users can install only the providers, templates, or development-agent helpers they need.

## Modules

| Module | Purpose | Install |
| --- | --- | --- |
| `agents/agentnode` | Wrap an A2A agent as a typed graph node with nested child-event evidence. | `go get github.com/gopact-ai/gopact-ext/agents/agentnode@v0.1.11` |
| `agents/agenttool` | Wrap an A2A agent as a regular `gopact.ToolFunc`. | `go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.30` |
| `agents/humanreview` | Add provider-neutral human approval gates to graph workflows with interrupt/resume support. | `go get github.com/gopact-ai/gopact-ext/agents/humanreview@v0.1.8` |
| `agents/planexec` | Plan-Execute template with replan, approval, checkpoint, and cancel support. | `go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.31` |
| `agents/react` | ReAct model/tool loop with memory, checkpoint, resume, and verification hooks. | `go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.29` |
| `agents/scheduler` | Run leased background jobs with retry, stop, dead-letter, drain, and schedule evidence. | `go get github.com/gopact-ai/gopact-ext/agents/scheduler@v0.1.8` |
| `agents/supervisor` | Route one task to a named child runnable while preserving event evidence. | `go get github.com/gopact-ai/gopact-ext/agents/supervisor@v0.1.17` |
| `devagent/filesnapshot` | Capture file size, mode, mtime, and hashes as reproducible engineering evidence. | `go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.28` |
| `devagent/gitdiff` | Capture worktree or staged git diffs for development-agent verification. | `go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.28` |
| `devagent/selfbootstrap` | Coordinate analyze, plan patch proposal policy, write, test, and review evidence into a self-bootstrap run export and verification report. | `go get github.com/gopact-ai/gopact-ext/devagent/selfbootstrap@v0.1.9` |
| `devagent/workspace` | Adapt a local repository into policy-approved plan patch apply, diff, file snapshot, command, and CI gate evidence. | `go get github.com/gopact-ai/gopact-ext/devagent/workspace@v0.1.10` |
| `models/openai` | OpenAI-shaped Chat Completions and Responses provider adapter. | `go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.31` |
| `models/ark` | Volcengine Ark SDK provider adapter with API-key and AK/SK paths. | `go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.29` |
| `models/agnes` | Agnes AI OpenAI-compatible Chat Completions provider adapter. | `go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.32` |
| `models/glm` | GLM/Zhipu AI OpenAI-compatible Chat Completions provider adapter for China and international endpoints. | `go get github.com/gopact-ai/gopact-ext/models/glm@v0.1.0` |

Submodule tags include the module path prefix, for example `models/openai/v0.5.31`.

## Usage

OpenAI-compatible services should use `models/openai`. The adapter owns the API path: `WithChatCompletionsAPI()` selects `/chat/completions`, and `WithResponsesAPI()` selects `/responses`.

```go
client, err := openai.NewClient(
	openai.ProviderOpenAI,
	"https://api.openai.com/v1",
	os.Getenv("GOPACT_LLM_TOKEN"),
	openai.WithResponsesAPI(),
	gopact.WithModel(os.Getenv("GOPACT_LLM_MODEL")),
	gopact.EnableStreaming(),
	gopact.EnableToolCalling(),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.UserMessage("Reply with one sentence.")),
	gopact.WithMaxOutputTokens(512),
	gopact.WithTemperature(0.2),
))
```

Agent templates depend on the core model contract, not on a specific provider:

```go
agent, err := react.NewModelAgent(
	client,
	react.WithMaxIterations(4),
	react.WithModelOptions(gopact.WithMaxOutputTokens(1024)),
)
if err != nil {
	return err
}

events, err := gopacttest.CollectEvents(agent.Run(ctx, "summarize the release status"))
```

## Verification

CI is mock-only and must not depend on real providers, `.env`, or external network access. Run the same gates locally before opening a pull request:

```bash
git diff --check
./scripts/self-bootstrap-mock-suite.sh
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

Real provider tests are opt-in through the `integration` build tag. The repository root supports local `.env` files, and `.env` must stay untracked.

```bash
cp .env.example .env
./scripts/local-agnes-integration.sh
(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/agnes && go test -tags=integration -count=1 ./...)
(cd models/glm && go test -tags=integration -count=1 ./...)
(cd tests/agents && go test -tags=integration -count=1 ./...)
```

Common OpenAI-shaped provider variables:

```bash
GOPACT_LLM_BASEURL=https://apihub.agnes-ai.com/v1
GOPACT_LLM_TOKEN=your-token
GOPACT_LLM_MODEL=agnes-2.0-flash
```

Provider-specific overrides:

```bash
GOPACT_AGNES_API_KEY=your-agnes-token
GOPACT_AGNES_SK=your-agnes-token
GOPACT_AGNES_HTTP_TIMEOUT=90s
GOPACT_AGNES_MAX_ATTEMPTS=2
GOPACT_ARK_API_KEY=your-ark-api-key
GOPACT_GLM_API_KEY=your-glm-api-key
GOPACT_GLM_BASEURL=https://open.bigmodel.cn/api/paas/v4
GOPACT_GLM_INTERNATIONAL_API_KEY=your-glm-international-api-key
GOPACT_GLM_INTERNATIONAL_BASEURL=https://api.z.ai/api/coding/paas/v4
GOPACT_GLM_MODEL=your-glm-model
GOPACT_OPENAI_API_KEY=your-openai-api-key
```

Use `models/ark` when testing the Volcengine Ark SDK path. Use `models/openai` when an Ark endpoint is being exercised as an OpenAI-compatible service; in that case, the API key belongs in `GOPACT_LLM_TOKEN`.
Use `models/glm` for GLM/Zhipu AI Chat Completions, selecting `NewClient` for the China Open Platform or `NewInternationalClient` for the international Z.AI endpoint.

## Documentation

- [doc/README.md](doc/README.md): documentation index.
- [doc/FEATURES.md](doc/FEATURES.md): executable feature matrix.
- [doc/CONTRIBUTING.md](doc/CONTRIBUTING.md): development setup, local checks, and pull request rules.
- [doc/SECURITY.md](doc/SECURITY.md): security policy and vulnerability reporting.
- [doc/CHANGELOG.md](doc/CHANGELOG.md): user-visible changes.
- [doc/maintainers/repository-governance.md](doc/maintainers/repository-governance.md): PR-only flow, CI gates, admin auto-merge, and public repository governance.

## Contributing

Extensions must keep provider-specific behavior inside their module, expose stable `gopact` contracts to callers, and document all required environment variables. The repository uses standard pull requests, CI status checks, and MIT licensing.
