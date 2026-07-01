# ark

Volcengine Ark Chat Completions provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.1
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
