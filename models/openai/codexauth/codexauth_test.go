package codexauth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		opts []codexauth.Option
	}{
		{name: "empty client id", opts: []codexauth.Option{codexauth.WithClientID("")}},
		{name: "invalid issuer", opts: []codexauth.Option{codexauth.WithIssuer("://bad")}},
		{name: "issuer credentials", opts: []codexauth.Option{codexauth.WithIssuer("https://user@example.com")}},
		{name: "issuer query", opts: []codexauth.Option{codexauth.WithIssuer("https://example.com?token=secret")}},
		{name: "insecure issuer", opts: []codexauth.Option{codexauth.WithIssuer("http://example.com")}},
		{name: "nil http client", opts: []codexauth.Option{codexauth.WithHTTPClient(nil)}},
		{name: "request timeout", opts: []codexauth.Option{codexauth.WithRequestTimeout(0)}},
		{name: "login timeout", opts: []codexauth.Option{codexauth.WithLoginTimeout(0)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := codexauth.New(tt.opts...); err == nil {
				t.Fatal("New() error = nil, want configuration error")
			}
		})
	}

	if _, err := codexauth.New(
		codexauth.WithIssuer("http://example.com"),
		codexauth.WithInsecureHTTP(),
	); err != nil {
		t.Fatalf("New(WithInsecureHTTP) error = %v", err)
	}
}

func TestClientStart(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/accounts/deviceauth/usercode" {
			t.Fatalf("request = %s %s, want device user-code request", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["client_id"] != codexauth.DefaultClientID {
			t.Fatalf("client_id = %q, want default Codex client ID", body["client_id"])
		}
		writeJSON(t, w, map[string]string{
			"device_auth_id": "device-123",
			"user_code":      "ABCD-EFGH",
			"interval":       "1",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	before := time.Now()
	code, err := client.Start(t.Context())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if code.VerificationURL != server.URL+"/codex/device" {
		t.Fatalf("VerificationURL = %q", code.VerificationURL)
	}
	if code.UserCode != "ABCD-EFGH" {
		t.Fatalf("UserCode = %q", code.UserCode)
	}
	if code.ExpiresAt.Before(before.Add(time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want future expiry", code.ExpiresAt)
	}
}

func TestClientStartReportsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Start(t.Context())
	if !errors.Is(err, codexauth.ErrDeviceLoginUnavailable) {
		t.Fatalf("Start() error = %v, want ErrDeviceLoginUnavailable", err)
	}
}

func TestClientStartAcceptsLegacyUserCodeField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]string{
			"device_auth_id": "device-123",
			"usercode":       "ABCD-EFGH",
			"interval":       "1",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	code, err := client.Start(t.Context())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if code.UserCode != "ABCD-EFGH" {
		t.Fatalf("UserCode = %q, want legacy usercode value", code.UserCode)
	}
}

func TestClientWaitExchangesTokens(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	idToken := testJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "account-123"},
	})
	accessToken := testJWT(t, map[string]any{"exp": expiresAt.Unix()})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]string{
				"device_auth_id": "device-123",
				"user_code":      "ABCD-EFGH",
				"interval":       "1",
			})
		case "/api/accounts/deviceauth/token":
			serveAuthorizedPoll(t, w, r)
		case "/oauth/token":
			serveTokenExchange(t, w, r, exchangeFixture{server.URL, idToken, accessToken})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	code, err := client.Start(t.Context())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	tokens, err := client.Wait(t.Context(), code)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if tokens.IDToken != idToken || tokens.AccessToken != accessToken || tokens.RefreshToken != "refresh-123" {
		t.Fatalf("tokens did not preserve exchanged values")
	}
	if tokens.AccountID != "account-123" {
		t.Fatalf("AccountID = %q, want account-123", tokens.AccountID)
	}
	if !tokens.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", tokens.ExpiresAt, expiresAt)
	}
}

func TestClientWaitTreatsPendingStatusesAsPending(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var polls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/accounts/deviceauth/usercode":
					writeJSON(t, w, map[string]string{
						"device_auth_id": "device-123",
						"user_code":      "ABCD-EFGH",
						"interval":       "60",
					})
				case "/api/accounts/deviceauth/token":
					servePendingPoll(w, &polls, cancel, status)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			code, err := client.Start(t.Context())
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			_, err = client.Wait(ctx, code)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Wait() error = %v, want context cancellation", err)
			}
			if got := polls.Load(); got != 1 {
				t.Fatalf("poll count = %d, want 1", got)
			}
		})
	}
}

func TestClientWaitRejectsInvalidDeviceCode(t *testing.T) {
	client, err := codexauth.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Wait(t.Context(), codexauth.DeviceCode{}); err == nil {
		t.Fatal("Wait() error = nil, want invalid device code error")
	}
}

func TestClientWaitRejectsDeviceCodeFromAnotherClient(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]string{
			"device_auth_id": "device-123",
			"user_code":      "ABCD-EFGH",
			"interval":       "1",
		})
	}))
	defer issuer.Close()

	var reached atomic.Bool
	otherIssuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer otherIssuer.Close()

	code, err := newTestClient(t, issuer.URL).Start(t.Context())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, err = newTestClient(t, otherIssuer.URL).Wait(t.Context(), code)
	if err == nil || !strings.Contains(err.Error(), "another client") {
		t.Fatalf("Wait() error = %v, want client-binding error", err)
	}
	if reached.Load() {
		t.Fatal("another issuer received private device-code state")
	}
}

func TestClientWaitAllowsMissingAccountIDClaim(t *testing.T) {
	idToken := testJWT(t, map[string]any{"sub": "user-123"})
	accessToken := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]string{
				"device_auth_id": "device-123",
				"user_code":      "ABCD-EFGH",
				"interval":       "1",
			})
		case "/api/accounts/deviceauth/token":
			serveAuthorizedPoll(t, w, r)
		case "/oauth/token":
			serveTokenExchange(t, w, r, exchangeFixture{server.URL, idToken, accessToken})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	code, err := client.Start(t.Context())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	tokens, err := client.Wait(t.Context(), code)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if tokens.AccountID != "" {
		t.Fatalf("AccountID = %q, want empty optional claim", tokens.AccountID)
	}
}

func TestClientWaitReportsExpiredDeviceCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]string{
			"device_auth_id": "device-123",
			"user_code":      "ABCD-EFGH",
			"interval":       "1",
		})
	}))
	defer server.Close()

	client, err := codexauth.New(
		codexauth.WithIssuer(server.URL),
		codexauth.WithInsecureHTTP(),
		codexauth.WithLoginTimeout(time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	code, err := client.Start(t.Context())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for time.Now().Before(code.ExpiresAt) {
	}
	_, err = client.Wait(t.Context(), code)
	if !errors.Is(err, codexauth.ErrDeviceCodeExpired) {
		t.Fatalf("Wait() error = %v, want ErrDeviceCodeExpired", err)
	}
}

func TestClientRefreshMergesRotatedTokens(t *testing.T) {
	expiresAt := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	newAccessToken := testJWT(t, map[string]any{"exp": expiresAt.Unix()})
	newIDToken := testJWT(t, map[string]any{"sub": "user-123"})
	idToken := testJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "account-123"},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh request: %v", err)
		}
		if body["client_id"] != codexauth.DefaultClientID || body["grant_type"] != "refresh_token" || body["refresh_token"] != "refresh-old" {
			t.Fatalf("refresh request = %#v", body)
		}
		writeJSON(t, w, map[string]string{
			"id_token":      newIDToken,
			"access_token":  newAccessToken,
			"refresh_token": "refresh-new",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	tokens, err := client.Refresh(t.Context(), codexauth.Tokens{
		IDToken:      idToken,
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		AccountID:    "account-123",
	})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if tokens.IDToken != newIDToken || tokens.AccountID != "account-123" {
		t.Fatalf("Refresh() lost stable identity fields: %#v", tokens)
	}
	if tokens.AccessToken != newAccessToken || tokens.RefreshToken != "refresh-new" {
		t.Fatalf("Refresh() tokens = %#v, want rotated values", tokens)
	}
	if !tokens.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", tokens.ExpiresAt, expiresAt)
	}
}

func TestClientRefreshRejectsJWTWithTrailingData(t *testing.T) {
	malformedIDToken := testRawJWT(t, `{"https://api.openai.com/auth":{"chatgpt_account_id":"account-123"}} {}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]string{"id_token": malformedIDToken})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Refresh(t.Context(), codexauth.Tokens{
		IDToken:      testJWT(t, map[string]any{"sub": "user-123"}),
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	})
	if err == nil {
		t.Fatal("Refresh() error = nil, want malformed JWT error")
	}
}

func TestClientRefreshRequiresRefreshToken(t *testing.T) {
	client, err := codexauth.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Refresh(t.Context(), codexauth.Tokens{}); err == nil {
		t.Fatal("Refresh() error = nil, want missing refresh token error")
	}
}

func TestClientRefreshRejectsCrossOriginRedirectWithoutSendingToken(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		body, _ := io.ReadAll(r.Body)
		t.Errorf("redirect target received body %q", body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := newTestClient(t, source.URL)
	_, err := client.Refresh(t.Context(), codexauth.Tokens{RefreshToken: "refresh-secret"})
	if err == nil {
		t.Fatal("Refresh() error = nil, want cross-origin redirect rejection")
	}
	if reached.Load() {
		t.Fatal("cross-origin redirect received refresh token")
	}
	if strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("Refresh() error leaked token: %v", err)
	}
}

func TestClientHTTPErrorDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"refresh-secret"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Refresh(t.Context(), codexauth.Tokens{RefreshToken: "refresh-secret"})
	if err == nil {
		t.Fatal("Refresh() error = nil, want HTTP error")
	}
	var httpErr *codexauth.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("Refresh() error = %T %v, want HTTPError status 500", err, err)
	}
	if strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("Refresh() error leaked response body: %v", err)
	}
}

func newTestClient(t *testing.T, issuer string) *codexauth.Client {
	t.Helper()
	client, err := codexauth.New(
		codexauth.WithIssuer(issuer),
		codexauth.WithInsecureHTTP(),
		codexauth.WithRequestTimeout(time.Second),
		codexauth.WithLoginTimeout(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func assertFormValue(t *testing.T, values url.Values, name, expected string) {
	t.Helper()
	if got := values.Get(name); got != expected {
		t.Fatalf("form value %q = %q, want %q", name, got, expected)
	}
}

func serveAuthorizedPoll(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode poll request: %v", err)
	}
	if body["device_auth_id"] != "device-123" || body["user_code"] != "ABCD-EFGH" {
		t.Fatalf("poll request = %#v", body)
	}
	writeJSON(t, w, map[string]string{
		"authorization_code": "authorization-123",
		"code_challenge":     "challenge-123",
		"code_verifier":      "verifier-123",
	})
}

type exchangeFixture struct {
	serverURL   string
	idToken     string
	accessToken string
}

func serveTokenExchange(t *testing.T, w http.ResponseWriter, r *http.Request, fixture exchangeFixture) {
	t.Helper()
	if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q, want form encoding", got)
	}
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse token form: %v", err)
	}
	assertFormValue(t, r.Form, "grant_type", "authorization_code")
	assertFormValue(t, r.Form, "code", "authorization-123")
	assertFormValue(t, r.Form, "redirect_uri", fixture.serverURL+"/deviceauth/callback")
	assertFormValue(t, r.Form, "client_id", codexauth.DefaultClientID)
	assertFormValue(t, r.Form, "code_verifier", "verifier-123")
	writeJSON(t, w, map[string]any{
		"id_token":      fixture.idToken,
		"access_token":  fixture.accessToken,
		"refresh_token": "refresh-123",
	})
}

func servePendingPoll(w http.ResponseWriter, polls *atomic.Int32, cancel context.CancelFunc, status int) {
	polls.Add(1)
	cancel()
	w.WriteHeader(status)
}

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return testRawJWT(t, string(payload))
}

func testRawJWT(t *testing.T, payload string) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	return fmt.Sprintf("%s.%s.%s", encode(header), encode([]byte(payload)), encode([]byte("signature")))
}
