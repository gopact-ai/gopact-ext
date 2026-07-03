# filesnapshot

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`filesnapshot` 将单个文件转换成 `gopacttest.FileSnapshot`，用于 dev-agent、release gate 或文档检查中固化工程证据。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.23
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
