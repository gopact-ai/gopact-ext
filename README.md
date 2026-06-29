# gopact-ext

Official extensions for `github.com/gopact-ai/gopact`.

This repository uses one Git repo with separate Go modules per extension, so users can depend on only what they need.

## Modules

- `models/openai`: OpenAI-shaped Chat Completions provider adapter.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/openai@v0.1.0
```

Extension modules are versioned with Go submodule tags such as `models/openai/v0.1.0`.

## Development

```bash
git diff --check
go test -count=1 ./models/openai/...
go vet ./models/openai/...
```
