# planexec

Minimal Plan-Execute agent template for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.13
```

## Scope

This module provides a small provider-neutral plan-execute graph with model-backed planning, one-shot replan via `WithReplanner`, approval interrupt/resume, checkpoint resume, and cancel propagation. Callers can pass any `gopact.ResponseModel`.

## Usage

```go
agent, err := planexec.NewModelAgent(
	model,
	planexec.WithModelOptions(gopact.WithMaxOutputTokens(1024)),
	planexec.WithApprovalPolicy(policy),
	planexec.WithCheckpointStore(checkpoints),
)
if err != nil {
	return err
}

for event, err := range agent.Run(ctx, "draft and review the release note") {
	if err != nil {
		return err
	}
	_ = event
}
```

Advanced callers can still use `New` with custom `Planner` and `Executor` implementations.
