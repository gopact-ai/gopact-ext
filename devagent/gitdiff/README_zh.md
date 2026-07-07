# gitdiff

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`gitdiff` 将 git worktree 或 staged diff 转换成 `gopacttest.DiffSnapshot`，用于 dev-agent 在执行前后记录可审计的代码变更证据。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.30
```

## 用法

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

`ScanWorktree` 读取 unstaged changes，`ScanStaged` 读取 staged changes。两者都会返回 diff、文件列表、insertions 和 deletions，不会执行 patch、commit 或 reset。

## 验证

```bash
(cd devagent/gitdiff && go test -count=1 ./...)
```
