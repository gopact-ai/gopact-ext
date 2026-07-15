# OpenAI Codex device authentication

Chinese documentation: [README_zh.md](README_zh.md)

`codexauth` implements the device-code login used by OpenAI Codex for ChatGPT plans. It requests a user code, waits for browser authorization, exchanges the authorization code for OAuth tokens, and refreshes those tokens when required.

This package is authentication-only. Pair it with [`models/openai/codex`](../codex) for model calls. It does not treat the implementation-level ChatGPT Codex backend as a generic OpenAI-compatible API, and it does not read or write credentials owned by Codex CLI.

## Login

```go
auth, err := codexauth.New()
if err != nil {
	return err
}

device, err := auth.Start(ctx)
if err != nil {
	return err
}

fmt.Printf("Open %s and enter %s\n", device.VerificationURL, device.UserCode)

tokens, err := auth.Wait(ctx, device)
if err != nil {
	return err
}

// Persist the complete value in an operating-system keychain or another
// equivalently protected secret store. Do not log it.
if err := secretStore.Save(ctx, tokens); err != nil {
	return err
}
```

`Wait` polls synchronously and stops when its context is canceled or the device code expires. `DeviceCode` also carries private protocol state, so pass the value returned by `Start` directly to `Wait`; it is not a resumable serialized login session.

`Tokens.AccountID` is populated from the ChatGPT claim when present and is otherwise empty. A missing optional account claim does not discard otherwise valid OAuth credentials.

## Refresh

OpenAI can rotate refresh tokens. Always replace the complete stored value with the value returned by `Refresh`:

```go
tokens, err = auth.Refresh(ctx, tokens)
if err != nil {
	return err
}
if err := secretStore.Save(ctx, tokens); err != nil {
	return err
}
```

Persist the replacement atomically when the secret store supports it. The package does not coordinate with `~/.codex/auth.json`; sharing that file with another process can race token refresh and overwrite rotated credentials.

For a long-running model provider, wrap the client and a `Store` in a refreshing `Source`:

```go
source, err := codexauth.NewSource(auth, secretStore)
```

`Source.Token` refreshes credentials shortly before expiry, while `Source.Refresh` forces a rotation after an unauthorized model response. Both persist the complete replacement before returning it. One `Source` serializes refreshes inside its process; a store shared across processes must provide its own cross-process coordination.

## Security boundaries

- HTTPS is required by default. `WithInsecureHTTP` is only for local development and tests.
- OAuth request bodies are never forwarded across origins during redirects.
- HTTP error response bodies are not retained in returned errors because they may contain authentication details.
- `Tokens` contains the ID token, access token, and refresh token. Never include it in logs, telemetry, checkpoints, or ordinary application configuration.
- Custom issuers are trusted authentication boundaries. JWT claims are decoded only to expose account identity and access-token expiry; this package does not independently verify signatures from a custom issuer.

The default issuer, client ID, and request sequence mirror the current [OpenAI Codex device authorization implementation](https://github.com/openai/codex/blob/main/codex-rs/login/src/device_code_auth.rs). These implementation-level endpoints can change upstream; both values can be overridden for compatible deployments and deterministic tests.
