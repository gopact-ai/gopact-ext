# gitdiff

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/devagent/gitdiff.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/devagent/gitdiff)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)


<!-- gopact:doc-language: zh,en -->

## 中文

本文档是 gopact 开源文档集的一部分，中文内容用于说明当前仓库约束、能力或维护流程。

## English

This document is part of the gopact open-source documentation set. The English section gives an entry point for readers who prefer English, while the remaining sections preserve the maintained technical details.


Git diff scanner for Dev Agent evidence collection.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.12
```

## Usage

```go
snapshot, err := gitdiff.ScanWorktree(ctx, ".")
if err != nil {
	return err
}
if snapshot.Skipped {
	return nil
}
return gopacttest.RecordDiffCheck(recorder, snapshot)
```

`ScanWorktree` reads unstaged changes. `ScanStaged` reads staged changes. Both return `gopacttest.DiffSnapshot` and leave verification, release decisions, and patch application to the caller.
