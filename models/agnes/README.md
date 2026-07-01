# agnes

Agnes AI text model provider adapter for `gopact`.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.12
```

## Usage

```go
client, err := agnes.New(
	appSecrets.AgnesAPIKey,
	agnes.EnableThinking(),
	gopact.WithMaxOutputTokens(1024),
)
if err != nil {
	return err
}

response, err := client.Generate(ctx, gopact.NewModelRequest(
	gopact.WithMessages(gopact.Message{
		Role:    gopact.RoleUser,
		Content: "Say hello",
	}),
))
if err != nil {
	return err
}
fmt.Println(response.Message.Text())
```

`DefaultBaseURL` is `https://apihub.agnes-ai.com/v1`, and `DefaultModel` is `agnes-2.0-flash`.
Thinking is sent as `chat_template_kwargs.enable_thinking` for Agnes OpenAI-compatible Chat Completions.
