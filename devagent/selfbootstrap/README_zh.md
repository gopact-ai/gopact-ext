# selfbootstrap

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`selfbootstrap` 提供 provider-neutral 的 Dev Agent workflow：analyze、plan、patch proposal policy 授权、write、test、review。每个阶段都由宿主注入，因此模块本身不会调用模型、执行命令、应用 patch 或读取工作区。它只把宿主已经观察到的 policy decision、command、CI gate、diff、file snapshot、review、run export、failure attribution 和 verification report 证据串成稳定结果。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/devagent/selfbootstrap@v0.1.8
```

## 用法

```go
workflow, err := selfbootstrap.New(
	selfbootstrap.WithAnalyzer(analyzer),
	selfbootstrap.WithPlanner(planner),
	selfbootstrap.WithPatchPolicy(policy),
	selfbootstrap.WithWriter(writer),
	selfbootstrap.WithTester(tester),
	selfbootstrap.WithReviewer(reviewer),
)
if err != nil {
	return err
}

result, err := workflow.Run(ctx, selfbootstrap.Request{
	Objective:  "ship a tested SDK slice",
	Repository: "gopact-ext",
	IDs:        gopact.RuntimeIDs{RunID: "devagent-run-1"},
})
```

## 验证

```bash
(cd devagent/selfbootstrap && go test -count=1 ./...)
```
