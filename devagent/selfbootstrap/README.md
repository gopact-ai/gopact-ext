# selfbootstrap

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/devagent/selfbootstrap.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/devagent/selfbootstrap)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`selfbootstrap` coordinates a provider-neutral Dev Agent workflow: analyze, plan, policy-authorize a patch proposal, write, test, and review. Each stage is injected by the host, so the module does not call models, execute commands, apply patches, or read a workspace by itself. It records observed policy decision, command, CI gate, diff, file snapshot, review, run export, failure attribution, and verification report evidence.

Install it with `go get github.com/gopact-ai/gopact-ext/devagent/selfbootstrap@v0.1.11`.

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

Run `(cd devagent/selfbootstrap && go test -count=1 ./...)` before changing behavior.
