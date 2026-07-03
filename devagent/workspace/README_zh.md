# workspace

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`workspace` 将本地仓库根目录适配为 self-bootstrap evidence。它组合 git diff、file snapshot、本地命令执行和 CI gate 映射，提供 `selfbootstrap.Writer` 与 `selfbootstrap.Tester` 实现。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/devagent/workspace@v0.1.0
```

## 用法

```go
ws, err := workspace.New("/path/to/repo")
if err != nil {
	return err
}

workflow, err := selfbootstrap.New(
	selfbootstrap.WithAnalyzer(analyzer),
	selfbootstrap.WithPlanner(planner),
	selfbootstrap.WithWriter(ws.Writer("go.mod", "README.md")),
	selfbootstrap.WithTester(ws.Tester(workspace.Command{
		Gate: gopacttest.SelfBootstrapCIGateUnit,
		Args: []string{"go", "test", "-count=1", "./..."},
	})),
	selfbootstrap.WithReviewer(reviewer),
)
```

该 adapter 记录 repo-relative path 和 command evidence。它只采集并转交证据，不替调用方判断 release 是否可接受。

## 验证

```bash
(cd devagent/workspace && go test -count=1 ./...)
```
