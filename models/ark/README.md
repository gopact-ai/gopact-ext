# ark

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/ark.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/models/ark)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)


<!-- gopact:doc-language: zh,en -->

## 中文

本文档是 gopact 开源文档集的一部分，中文内容用于说明当前仓库约束、能力或维护流程。

## English

This document is part of the gopact open-source documentation set. The English section gives an entry point for readers who prefer English, while the remaining sections preserve the maintained technical details.


Volcengine Ark Chat Completions provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.13
```

## Usage

```go
client, err := ark.New(ark.Options{
	BaseURL:   "https://ark.cn-beijing.volces.com",
	Region:    ark.DefaultRegion,
	AccessKey: appSecrets.ArkAccessKey,
	SecretKey: appSecrets.ArkSecretKey,
})
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.ModelRequest{
	Model: "ep-...",
	Messages: []gopact.Message{{
		Role:    gopact.RoleUser,
		Content: "Say hello",
	}},
})
if err != nil {
	return err
}
fmt.Println(response.Message.Text())
```

`BaseURL` defaults to `https://ark.cn-beijing.volces.com/api/v3`, and values without `/api/v3` are normalized.
