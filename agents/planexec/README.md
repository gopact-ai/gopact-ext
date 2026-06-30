# planexec

Minimal Plan-Execute agent template for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.1.0
```

## Scope

This module provides a small provider-neutral plan-execute graph. Callers inject a `Planner` and an `Executor`; the template emits normal `gopact` graph events for `plan`, `execute`, and `summarize` nodes.
