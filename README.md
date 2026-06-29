# gopact-ext

Official extensions for `github.com/gopact-ai/gopact`.

This repository uses one Git repo with separate Go modules per extension, so users can depend on only what they need.

## Modules

- `models/openaicompatible`: OpenAI-compatible Chat Completions provider adapter.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/openaicompatible
```

## Development

```bash
git diff --check
go test -count=1 ./models/openaicompatible/...
go vet ./models/openaicompatible/...
```
