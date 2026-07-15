# OpenAI Codex 设备码认证

English documentation: [README.md](README.md)

`codexauth` 实现 OpenAI Codex 面向 ChatGPT plan 的设备码登录：申请 user code、等待浏览器授权、用 authorization code 换取 OAuth token，并在需要时刷新 token。

本包只负责认证。它不会把 ChatGPT Codex 私有 backend 当作通用 OpenAI-compatible 模型 API，也不会读取或写入 Codex CLI 管理的凭据。

## 登录

```go
auth, err := codexauth.New()
if err != nil {
	return err
}

device, err := auth.Start(ctx)
if err != nil {
	return err
}

fmt.Printf("打开 %s 并输入 %s\n", device.VerificationURL, device.UserCode)

tokens, err := auth.Wait(ctx, device)
if err != nil {
	return err
}

// 把完整值保存到操作系统钥匙串或具备同等保护能力的 secret store，
// 不要记录到日志。
if err := secretStore.Save(ctx, tokens); err != nil {
	return err
}
```

`Wait` 采用同步轮询，并在 context 取消或设备码过期时停止。`DeviceCode` 还包含私有协议状态，因此应把 `Start` 返回的值直接交给 `Wait`；它不是可序列化后恢复的登录 session。

`Tokens.AccountID` 会在 ChatGPT claim 存在时填充，否则为空；缺少这个可选 claim 不会丢弃其他有效的 OAuth 凭据。

## 刷新

OpenAI 可能轮换 refresh token。每次都要用 `Refresh` 返回的完整值替换旧值：

```go
tokens, err = auth.Refresh(ctx, tokens)
if err != nil {
	return err
}
if err := secretStore.Save(ctx, tokens); err != nil {
	return err
}
```

secret store 支持时应原子替换。该包不会与 `~/.codex/auth.json` 协调；让多个进程共享这个文件可能造成 refresh 竞争，并覆盖轮换后的凭据。

## 安全边界

- 默认只允许 HTTPS；`WithInsecureHTTP` 仅用于本地开发和测试。
- OAuth request body 不会在 redirect 时跨 origin 转发。
- HTTP 错误不会保留 response body，因为其中可能包含认证细节。
- `Tokens` 包含 ID token、access token 和 refresh token，禁止写入日志、遥测、checkpoint 或普通应用配置。
- 自定义 issuer 是受信认证边界。JWT claim 只用于提取 account identity 和 access-token 过期时间；本包不会独立验证自定义 issuer 签发 token 的签名。

默认 issuer、client ID 和请求顺序与当前 [OpenAI Codex 设备授权实现](https://github.com/openai/codex/blob/main/codex-rs/login/src/device_code_auth.rs) 保持一致。这些实现级 endpoint 可能随上游变化；兼容部署和确定性测试可以显式覆盖相关配置。
