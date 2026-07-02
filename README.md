# gopact-ext

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](LICENSE)
[![OpenAI Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/openai.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/openai)

<!-- gopact:doc-language: zh,en -->

## 中文

`gopact-ext` 是 `github.com/gopact-ai/gopact` 的官方 extension 仓库。仓库采用一个 Git repo、多个 Go submodule 的结构，用户只需要依赖自己使用的 provider、agent template 或 dev-agent helper。

## 模块

- `agents/agenttool`：把 A2A agent 暴露为 tool。
- `agents/planexec`：Plan-Execute agent template。
- `agents/react`：ReAct 风格的 model/tool loop agent template。
- `devagent/filesnapshot`：Dev Agent evidence collection 的文件快照扫描器。
- `devagent/gitdiff`：Dev Agent evidence collection 的 git diff 扫描器。
- `models/agnes`：Agnes AI OpenAI-compatible text model provider adapter。
- `models/ark`：Volcengine Ark Chat Completions provider adapter。
- `models/openai`：OpenAI-shaped Chat Completions and Responses provider adapter。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.14
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.15
go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.13
go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.12
go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.12
go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.15
go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.13
go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.16
```

Extension modules 使用 Go submodule tag，例如 `models/openai/v0.5.0`。

## 文档索引

- [doc/README.md](./doc/README.md)：完整文档索引。
- [doc/FEATURES.md](./doc/FEATURES.md)：可执行能力覆盖矩阵。
- [doc/CONTRIBUTING.md](./doc/CONTRIBUTING.md)：贡献指南。
- [doc/SECURITY.md](./doc/SECURITY.md)：安全策略。
- [doc/CHANGELOG.md](./doc/CHANGELOG.md)：变更记录。
- [doc/maintainers/repository-governance.md](./doc/maintainers/repository-governance.md)：PR、CI、自动合并和公开前检查规则。

## 开发

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

## 集成测试

Provider modules include opt-in real-service tests behind the `integration` build tag:

```bash
cp .env.example .env
(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/agnes && go test -tags=integration -count=1 ./...)
(cd tests/agents && go test -tags=integration -count=1 ./...)
```

测试会在存在 `.env` 时从仓库根目录加载。`.env` 必须保持本地文件。Agnes 和 Ark 支持共享的 `GOPACT_LLM_BASEURL`、`GOPACT_LLM_TOKEN`、`GOPACT_LLM_MODEL`，也支持 provider-specific key：`GOPACT_AGNES_API_KEY`、`GOPACT_AGNES_SK`、`GOPACT_ARK_API_KEY`、`GOPACT_OPENAI_API_KEY`。

## English

`gopact-ext` contains the official extensions for `github.com/gopact-ai/gopact`. It is one Git repository with separate Go modules, so users can depend only on the providers, agent templates, or development-agent helpers they need.

See [doc/README.md](./doc/README.md) for the complete documentation index and [doc/FEATURES.md](./doc/FEATURES.md) for the executable capability matrix. CI is mock-only by default; real provider checks are opt-in through the `integration` build tag and local `.env` configuration.
