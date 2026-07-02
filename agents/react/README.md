# react

ReAct-style model/tool loop agent template for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.12
```

## Scope

This module externalizes the ReAct template from core. It keeps the template provider-neutral: callers can pass any `gopact.ResponseModel`.

## Usage

```go
agent, err := react.NewModelAgent(
	model,
	react.WithTools(ctx, uppercaseTool),
	react.WithModelOptions(
		gopact.WithMaxOutputTokens(1024),
		gopact.WithTemperature(0.2),
	),
)
if err != nil {
	return err
}

for event, err := range agent.Run(ctx, "uppercase gopact and answer briefly") {
	if err != nil {
		return err
	}
	_ = event
}
```

Advanced callers can still use `New` with a custom `gopact.ChatModel` and `tools.Registry`.
