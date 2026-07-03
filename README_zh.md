# gopact-ext

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`gopact-ext` 是 `github.com/gopact-ai/gopact` 的官方扩展仓库。它采用一个 Git 仓库、多个 Go submodule 的结构，把 provider、agent template 和 dev-agent helper 放在同一个治理面下，同时让用户只安装自己需要的模块。

这个仓库的定位很明确：

- `models/*` 负责把真实模型服务适配到 core 的 `gopact.ModelRequest`、`Generate`、`Stream`、tool calling、structured output 和错误分类契约。
- `agents/*` 负责沉淀常用 agent 范式，例如 ReAct、Plan-Execute，以及把 A2A agent 暴露为 tool。
- `devagent/*` 负责把代码、文件、diff 等工程证据转换成可验证的测试输入。
- `tests/agents` 负责跨模块模板组合测试；CI 默认只跑 mock，真实 provider 测试必须显式启用。

## 模块

| 模块 | 用途 | 安装 |
| --- | --- | --- |
| `agents/agentnode` | 将 A2A agent 包装成 typed graph node，并保留嵌套 child-event evidence。 | `go get github.com/gopact-ai/gopact-ext/agents/agentnode@v0.1.7` |
| `agents/agenttool` | 将 A2A agent 包装成普通 `gopact.ToolFunc`。 | `go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.26` |
| `agents/humanreview` | 为 graph workflow 提供 provider-neutral 的人工审批 gate，支持 interrupt/resume。 | `go get github.com/gopact-ai/gopact-ext/agents/humanreview@v0.1.4` |
| `agents/planexec` | Plan-Execute agent template，支持 replan、approval、checkpoint、cancel。 | `go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.27` |
| `agents/react` | ReAct model/tool loop template，支持 memory、checkpoint、resume、verification。 | `go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.25` |
| `agents/scheduler` | 支持 leased background jobs、retry、stop、dead-letter、drain 和 schedule evidence。 | `go get github.com/gopact-ai/gopact-ext/agents/scheduler@v0.1.4` |
| `agents/supervisor` | 将任务路由到指定子 runnable，并保留 event evidence。 | `go get github.com/gopact-ai/gopact-ext/agents/supervisor@v0.1.13` |
| `devagent/filesnapshot` | 采集文件 hash、大小、mode、mtime，用于工程证据固化。 | `go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.24` |
| `devagent/gitdiff` | 采集 worktree 或 staged git diff，用于开发 agent 验证。 | `go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.24` |
| `devagent/selfbootstrap` | 编排 analyze、plan patch proposal policy、write、test、review 证据，产出 self-bootstrap run export 和 verification report。 | `go get github.com/gopact-ai/gopact-ext/devagent/selfbootstrap@v0.1.5` |
| `devagent/workspace` | 将本地仓库适配为 self-bootstrap 的 policy-approved plan patch apply、diff、file snapshot、command 和 CI gate evidence。 | `go get github.com/gopact-ai/gopact-ext/devagent/workspace@v0.1.6` |
| `models/openai` | OpenAI-shaped Chat Completions / Responses provider adapter。 | `go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.27` |
| `models/ark` | Volcengine Ark SDK provider adapter，支持 API key 或 AK/SK。 | `go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.25` |
| `models/agnes` | Agnes AI provider adapter，基于 OpenAI-compatible Chat Completions。 | `go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.28` |

Go submodule tag 使用模块路径前缀，例如 `models/openai/v0.5.27`。

## 快速开始

OpenAI-compatible 服务通过 `models/openai` 接入。API path 由 provider 模块内部决定：`WithChatCompletionsAPI()` 使用 `/chat/completions`，`WithResponsesAPI()` 使用 `/responses`，调用方只传 base URL、token、model 和请求参数。

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

Agent template 使用 core 的模型契约，不绑定具体 provider：

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

## 本地验证

CI 是 mock-only，不能依赖真实 provider、`.env` 或外部网络。提交 PR 前至少执行以下命令；这些命令也是 CI 契约的一部分。

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

## 真实 provider 测试

真实服务测试通过 `integration` build tag 手动执行，仓库根目录支持 `.env`，但 `.env` 必须保持本地文件。

```bash
cp .env.example .env
./scripts/local-agnes-integration.sh
(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/agnes && go test -tags=integration -count=1 ./...)
(cd tests/agents && go test -tags=integration -count=1 ./...)
```

通用 OpenAI-shaped provider 环境变量：

```bash
GOPACT_LLM_BASEURL=https://apihub.agnes-ai.com/v1
GOPACT_LLM_TOKEN=your-token
GOPACT_LLM_MODEL=agnes-2.0-flash
```

provider-specific override：

```bash
GOPACT_AGNES_API_KEY=your-agnes-token
GOPACT_AGNES_SK=your-agnes-token
GOPACT_ARK_API_KEY=your-ark-api-key
GOPACT_OPENAI_API_KEY=your-openai-api-key
```

Ark 的两条路径需要区分：`models/ark` 使用 Volcengine Ark SDK，可用 `APIKey` 或 AK/SK；如果某个 Ark endpoint 只是作为 OpenAI-compatible 服务测试，则应使用 `models/openai` 并把 token 放到 `GOPACT_LLM_TOKEN`。

## 文档索引

- [doc/README.md](./doc/README.md)：文档地图与推荐阅读顺序。
- [doc/FEATURES.md](./doc/FEATURES.md)：可执行能力覆盖矩阵。
- [doc/CONTRIBUTING.md](./doc/CONTRIBUTING.md)：贡献流程、本地验证和 PR 要求。
- [doc/SECURITY.md](./doc/SECURITY.md)：安全策略与漏洞报告方式。
- [doc/CHANGELOG.md](./doc/CHANGELOG.md)：变更记录。
- [doc/maintainers/repository-governance.md](./doc/maintainers/repository-governance.md)：PR-only、CI 门禁、admin auto-merge 和公开前检查。
