# humanreview

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/humanreview.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/humanreview)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`humanreview` provides provider-neutral approval-gate graph nodes. A gate maps typed graph state to a `gopact.InterruptRecord`, stops execution with `gopact.ErrInterrupted`, and lets the graph continue from the approved boundary through step-export or checkpoint resume.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/humanreview@v0.1.10`.

```go
gate, err := humanreview.New(func(_ context.Context, state ReleaseState) (humanreview.Request, error) {
	return humanreview.Request{
		ID:         "release:" + state.ID,
		Reason:     "release approval required",
		RequiredBy: "release-manager",
		Prompt:     gopact.UserMessage("Approve release " + state.ID + "?"),
	}, nil
})
if err != nil {
	return err
}

g := graph.New[ReleaseState]()
g.AddNode("review", gate)
g.AddNode("publish", publishRelease)
g.AddEdge(graph.Start, "review")
g.AddEdge("review", "publish")
g.AddEdge("publish", graph.End)
```

On resume, core graph semantics continue from the interrupted step output and downstream queue. The gate node is not rerun, and the resume payload is not automatically written back into state. Put review decisions in the caller's approval channel, event store, or downstream node contract when that data must be persisted.

Run `(cd agents/humanreview && go test -count=1 ./...)` before changing behavior.
