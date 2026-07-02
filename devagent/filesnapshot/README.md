# filesnapshot

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/devagent/filesnapshot.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/devagent/filesnapshot)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`filesnapshot` converts one local file into a `gopacttest.FileSnapshot` for dev-agent and release-gate evidence. It records hash and file metadata only; callers decide how that evidence is verified.

Install it with `go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.17`.
