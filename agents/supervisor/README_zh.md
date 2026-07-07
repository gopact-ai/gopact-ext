# supervisor

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/supervisor.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/supervisor)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: zh -->

[英文文档](README.md)

`supervisor` 把一个任务路由到指定的子 `gopact.Runnable`，并保留 run event 和 runtime IDs。路由策略由注入的 `Router` 负责；子 agent 自己负责 plan、tool、checkpoint 和 retry。

安装：

```bash
go get github.com/gopact-ai/gopact-ext/agents/supervisor@v0.2.1
```

验证：

```bash
(cd agents/supervisor && go test -count=1 ./...)
```
