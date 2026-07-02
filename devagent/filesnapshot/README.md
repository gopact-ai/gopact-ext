# filesnapshot

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/devagent/filesnapshot.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/devagent/filesnapshot)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: zh,en -->

## 中文

`filesnapshot` 将单个文件转换成 `gopacttest.FileSnapshot`，用于 dev-agent、release gate 或文档检查中固化工程证据。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.12
```

## 用法

```go
snapshot, err := filesnapshot.Scan(ctx, "go.mod")
if err != nil {
	return err
}
return gopacttest.RecordFileSnapshotCheck(recorder, snapshot)
```

`Scan` 记录 path、SHA-256 hash、size、mode 和 modified time。它只采集证据，不判断发布是否通过，也不修改文件。

## 验证

```bash
(cd devagent/filesnapshot && go test -count=1 ./...)
```

## English

`filesnapshot` converts one local file into a `gopacttest.FileSnapshot` for dev-agent and release-gate evidence. It records hash and file metadata only; callers decide how that evidence is verified.

Install it with `go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.12`.
