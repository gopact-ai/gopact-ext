# planexec

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/planexec.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/planexec)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)


<!-- gopact:doc-language: zh,en -->

## 中文

本文档是 gopact 开源文档集的一部分，中文内容用于说明当前仓库约束、能力或维护流程。

## English

This document is part of the gopact open-source documentation set. The English section gives an entry point for readers who prefer English, while the remaining sections preserve the maintained technical details.


Minimal Plan-Execute agent template for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.15
```

## Scope

This module provides a small provider-neutral plan-execute graph with model-backed planning, one-shot replan via `WithReplanner`, approval interrupt/resume, checkpoint resume, and cancel propagation. Callers can pass any `gopact.ResponseModel`.

## Usage

```go
agent, err := planexec.NewModelAgent(
	model,
	planexec.WithModelOptions(gopact.WithMaxOutputTokens(1024)),
	planexec.WithApprovalPolicy(policy),
	planexec.WithCheckpointStore(checkpoints),
)
if err != nil {
	return err
}

for event, err := range agent.Run(ctx, "draft and review the release note") {
	if err != nil {
		return err
	}
	_ = event
}
```

Advanced callers can still use `New` with custom `Planner` and `Executor` implementations.
