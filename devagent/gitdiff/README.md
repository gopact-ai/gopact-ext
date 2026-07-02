# gitdiff

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/devagent/gitdiff.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/devagent/gitdiff)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`gitdiff` converts worktree or staged git diffs into `gopacttest.DiffSnapshot` evidence. It reads diff data and statistics only; callers decide whether a change is acceptable.

Install it with `go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.13`.
