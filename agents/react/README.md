# react

ReAct-style model/tool loop agent template for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/agents/react@agents/react/v0.1.0
```

## Scope

This module externalizes the ReAct template from core. It keeps the template provider-neutral: callers inject a `gopact.ChatModel` and a `tools.Registry`.
