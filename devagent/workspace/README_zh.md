# workspace

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`workspace` 将本地仓库根目录适配为 self-bootstrap evidence。它组合受控 patch apply、git diff、file snapshot、本地命令执行和 CI gate 映射，提供 `selfbootstrap.Writer` 与 `selfbootstrap.Tester` 实现。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/devagent/workspace@v0.1.7
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
	selfbootstrap.WithPatchPolicy(policy),
	selfbootstrap.WithWriter(ws.PlanPatchWriter("go.mod", "README.md")),
	selfbootstrap.WithTester(ws.Tester(workspace.Command{
		Gate: gopacttest.SelfBootstrapCIGateUnit,
		Args: []string{"go", "test", "-count=1", "./..."},
	})),
	selfbootstrap.WithReviewer(reviewer),
)
```

当其他宿主组件已经完成修改时，使用 `Writer(paths...)` 只采集 evidence。当宿主希望 adapter 应用调用方提供的 unified diff 后再采集 evidence 时，使用 `PatchWriter(patch, paths...)`。当 self-bootstrap planner 产出 `PatchProposal` 时，使用 `PlanPatchWriter(paths...)`；它必须先看到 `WithPatchPolicy` 产生的 allow `PatchDecision`，才会应用 patch。

该 adapter 在应用 patch 前会校验路径，拒绝越过 repository root 的路径和 symlink escape，并记录 repo-relative path 和 command evidence。它不替调用方判断 release 是否可接受。

## 验证

```bash
(cd devagent/workspace && go test -count=1 ./...)
```
