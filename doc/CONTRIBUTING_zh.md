# Contributing to gopact-ext

<!-- gopact:doc-language: zh -->

[英文文档](./CONTRIBUTING.md)

## 中文

`gopact-ext` 是多 Go module 仓库。每个 extension 必须能被独立安装、测试和发布；跨模块测试只放在 `tests/agents`，不能让用户为了使用一个 provider 被迫拉入所有模板或测试依赖。

## Development Setup

前置工具：

- Go 1.25.11
- Git
- `golangci-lint` v2.8.0
- `govulncheck` v1.1.4

克隆后先跑最小验证：

```bash
git clone git@github.com:gopact-ai/gopact-ext.git
cd gopact-ext
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -count=1 ./...); done
```

修改规则：

- provider-specific API path、鉴权、请求转换、thinking/reasoning、tool calling、structured output 和错误分类必须封装在 provider 模块里。
- agent template 必须只依赖 core 契约，不能绑定某个具体 provider。
- CI 必须保持 mock-only；真实 provider 测试只能放在 `integration` build tag 下。
- `.env`、真实 token、真实 endpoint ID、原始 prompt、原始 provider response、客户数据不得进入仓库。
- 改公开 API、安装版本、功能边界时，同时更新根 README、对应模块 README 和 [FEATURES.md](FEATURES.md)。

## Verification

提交 PR 前运行：

```bash
git diff --check
./scripts/public-readiness-check.sh
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go mod tidy); done
git diff --exit-code
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -race -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go vet ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && golangci-lint run ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -coverprofile=coverage.out ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && govulncheck ./...); done
```

真实 provider 测试只在本地显式执行：

```bash
(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)
(cd models/agnes && go test -tags=integration -count=1 ./...)
(cd tests/agents && go test -tags=integration -count=1 ./...)
```

## Pull Request Checklist

- 变更有对应 mock 测试；真实 provider 行为有 integration 测试或明确说明无法自动验证的原因。
- 文档中的安装版本、模块路径、环境变量和测试命令仍然可执行。
- `go mod tidy` 后没有无关 diff。
- CI 仍是 mock-only，没有读取 `.env`、真实 token 或外部 provider。
- PR 不包含真实密钥、真实 endpoint ID、原始模型输出、私有 prompt 或用户数据。
