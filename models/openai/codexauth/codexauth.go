// Package codexauth implements OpenAI Codex device-code authentication.
//
// It returns OAuth tokens to the caller but deliberately does not persist them
// or read credentials managed by Codex CLI. Applications remain responsible for
// storing tokens in an appropriate secret store and persisting rotated refresh
// tokens returned by Refresh.
package codexauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultIssuer is the OpenAI OAuth issuer used by Codex.
	DefaultIssuer = "https://auth.openai.com"
	// DefaultClientID is the public OAuth client identifier used by Codex.
	DefaultClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	defaultRequestTimeout = 30 * time.Second
	defaultLoginTimeout   = 15 * time.Minute
	defaultPollInterval   = 5 * time.Second
	defaultMaxRedirects   = 10
	maxResponseBytes      = 1 << 20
	maxJWTClaimsBytes     = 64 << 10
	maxPollInterval       = 24 * time.Hour
	jwtPartCount          = 3
	jwtPayloadPart        = 1
	jwtSignaturePart      = 2
	quotedValueMinBytes   = 2
	decimalBase           = 10
	signedIntBits         = 64
)

var (
	// ErrDeviceLoginUnavailable reports that the issuer does not expose the
	// Codex device-code flow.
	ErrDeviceLoginUnavailable = errors.New("codexauth: device login unavailable")
	// ErrDeviceCodeExpired reports that authorization was not completed before
	// the device code expired.
	ErrDeviceCodeExpired = errors.New("codexauth: device code expired")
)

// HTTPError reports a non-success response without retaining its potentially
// sensitive response body.
type HTTPError struct {
	Operation  string
	StatusCode int
}

// Error implements error.
func (e *HTTPError) Error() string {
	if e == nil {
		return "codexauth: http error"
	}
	return fmt.Sprintf("codexauth: %s: status %d", e.Operation, e.StatusCode)
}

// DeviceCode contains the user-visible part of an in-progress login. Values
// must be passed back to a compatibly configured Client.Wait call without
// serialization because the type also carries private protocol state.
type DeviceCode struct {
	VerificationURL string
	UserCode        string
	ExpiresAt       time.Time

	deviceAuthID string
	pollInterval time.Duration
	issuer       string
	clientID     string
}

// Tokens are the credentials returned by OpenAI after a successful login.
// They contain secrets and must not be logged. Applications that persist this
// value should use an operating-system keychain or an equivalently protected
// secret store. AccountID is empty when the ID token omits that optional claim.
type Tokens struct {
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Client performs the Codex device-code and refresh-token flows. A Client is
// safe for concurrent use after New returns.
type Client struct {
	issuer         string
	clientID       string
	httpClient     *http.Client
	requestTimeout time.Duration
	loginTimeout   time.Duration
	allowHTTP      bool
	configErr      error
}

// Option configures a Client.
type Option func(*Client)

// WithIssuer overrides the OAuth issuer. HTTPS is required unless
// WithInsecureHTTP is also supplied.
func WithIssuer(issuer string) Option {
	return func(client *Client) {
		client.issuer = strings.TrimRight(issuer, "/")
	}
}

// WithClientID overrides the public OAuth client identifier.
func WithClientID(clientID string) Option {
	return func(client *Client) {
		client.clientID = clientID
	}
}

// WithHTTPClient sets the HTTP client. The value is copied and its redirect
// policy is restricted so OAuth payloads cannot cross origins.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient == nil {
			client.configErr = errors.Join(client.configErr, errors.New("codexauth: http client is nil"))
			return
		}
		client.httpClient = httpClient
	}
}

// WithRequestTimeout bounds each individual HTTP request.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(client *Client) {
		client.requestTimeout = timeout
	}
}

// WithLoginTimeout bounds the lifetime of a device code.
func WithLoginTimeout(timeout time.Duration) Option {
	return func(client *Client) {
		client.loginTimeout = timeout
	}
}

// WithInsecureHTTP allows an HTTP issuer for local development and tests.
func WithInsecureHTTP() Option {
	return func(client *Client) {
		client.allowHTTP = true
	}
}

// New creates a Codex device authentication client.
func New(opts ...Option) (*Client, error) {
	client := &Client{
		issuer:         DefaultIssuer,
		clientID:       DefaultClientID,
		httpClient:     http.DefaultClient,
		requestTimeout: defaultRequestTimeout,
		loginTimeout:   defaultLoginTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	issuer, err := client.validate()
	if err != nil {
		return nil, err
	}
	client.httpClient = secureHTTPClient(client.httpClient, issuer)
	return client, nil
}

func (c *Client) validate() (*url.URL, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}
	if c.clientID == "" {
		return nil, errors.New("codexauth: client id is required")
	}
	if c.httpClient == nil {
		return nil, errors.New("codexauth: http client is nil")
	}
	if c.requestTimeout <= 0 {
		return nil, errors.New("codexauth: request timeout must be positive")
	}
	if c.loginTimeout <= 0 {
		return nil, errors.New("codexauth: login timeout must be positive")
	}
	issuer, err := url.Parse(c.issuer)
	if err != nil || issuer.Host == "" || (issuer.Scheme != "http" && issuer.Scheme != "https") {
		return nil, errors.New("codexauth: issuer is invalid")
	}
	if issuer.Scheme == "http" && !c.allowHTTP {
		return nil, errors.New("codexauth: http issuer requires WithInsecureHTTP")
	}
	if issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || issuer.Opaque != "" {
		return nil, errors.New("codexauth: issuer must not contain credentials, query, or fragment")
	}
	return issuer, nil
}

// Start requests a device code that the application should present to the
// user. The returned value is valid until ExpiresAt.
func (c *Client) Start(ctx context.Context) (DeviceCode, error) {
	if c == nil {
		return DeviceCode{}, errors.New("codexauth: client is nil")
	}
	body, err := json.Marshal(userCodeRequest{ClientID: c.clientID})
	if err != nil {
		return DeviceCode{}, fmt.Errorf("codexauth: encode user-code request: %w", err)
	}
	resp, cancel, err := c.post(ctx, c.endpoint("/api/accounts/deviceauth/usercode"), "application/json", body)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("codexauth: request device code: %w", err)
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return DeviceCode{}, fmt.Errorf("codexauth: start device login: %w", ErrDeviceLoginUnavailable)
	}
	if !successfulStatus(resp.StatusCode) {
		return DeviceCode{}, &HTTPError{Operation: "start device login", StatusCode: resp.StatusCode}
	}
	var payload userCodeResponse
	if err := decodeResponse(resp.Body, &payload); err != nil {
		return DeviceCode{}, fmt.Errorf("codexauth: decode user-code response: %w", err)
	}
	if payload.UserCode == "" {
		payload.UserCode = payload.LegacyUserCode
	}
	if payload.DeviceAuthID == "" || payload.UserCode == "" {
		return DeviceCode{}, errors.New("codexauth: user-code response is incomplete")
	}
	interval := time.Duration(payload.Interval)
	if interval <= 0 {
		interval = defaultPollInterval
	}
	return DeviceCode{
		VerificationURL: c.endpoint("/codex/device"),
		UserCode:        payload.UserCode,
		ExpiresAt:       time.Now().Add(c.loginTimeout),
		deviceAuthID:    payload.DeviceAuthID,
		pollInterval:    interval,
		issuer:          c.issuer,
		clientID:        c.clientID,
	}, nil
}

// Wait polls until the user authorizes the device code, then exchanges the
// returned authorization code for OAuth tokens. It returns promptly when ctx
// is canceled.
func (c *Client) Wait(ctx context.Context, code DeviceCode) (Tokens, error) {
	if c == nil {
		return Tokens{}, errors.New("codexauth: client is nil")
	}
	if code.deviceAuthID == "" || code.UserCode == "" || code.pollInterval <= 0 || code.ExpiresAt.IsZero() ||
		code.issuer == "" || code.clientID == "" {
		return Tokens{}, errors.New("codexauth: device code is invalid")
	}
	if code.issuer != c.issuer || code.clientID != c.clientID {
		return Tokens{}, errors.New("codexauth: device code was issued for another client")
	}
	if !time.Now().Before(code.ExpiresAt) {
		return Tokens{}, ErrDeviceCodeExpired
	}
	parentCtx := nonNilContext(ctx)
	waitCtx, cancel := context.WithDeadlineCause(parentCtx, code.ExpiresAt, ErrDeviceCodeExpired)
	defer cancel()

	for {
		result, isPending, err := c.poll(waitCtx, code)
		if err != nil {
			return Tokens{}, err
		}
		if !isPending {
			return c.exchange(parentCtx, result)
		}
		if err := waitForPoll(waitCtx, code.pollInterval); err != nil {
			return Tokens{}, err
		}
	}
}

// Refresh exchanges a refresh token for rotated credentials. OpenAI may omit
// unchanged fields, so Refresh merges the response into current. Callers must
// persist the returned value before discarding current.
func (c *Client) Refresh(ctx context.Context, current Tokens) (Tokens, error) {
	if c == nil {
		return Tokens{}, errors.New("codexauth: client is nil")
	}
	if current.RefreshToken == "" {
		return Tokens{}, errors.New("codexauth: refresh token is required")
	}
	body, err := json.Marshal(refreshRequest{
		ClientID:     c.clientID,
		GrantType:    "refresh_token",
		RefreshToken: current.RefreshToken,
	})
	if err != nil {
		return Tokens{}, fmt.Errorf("codexauth: encode refresh request: %w", err)
	}
	resp, cancel, err := c.post(ctx, c.endpoint("/oauth/token"), "application/json", body)
	if err != nil {
		return Tokens{}, fmt.Errorf("codexauth: refresh tokens: %w", err)
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if !successfulStatus(resp.StatusCode) {
		return Tokens{}, &HTTPError{Operation: "refresh tokens", StatusCode: resp.StatusCode}
	}
	var payload tokenResponse
	if err := decodeResponse(resp.Body, &payload); err != nil {
		return Tokens{}, fmt.Errorf("codexauth: decode refresh response: %w", err)
	}
	if payload.IDToken == "" && payload.AccessToken == "" && payload.RefreshToken == "" {
		return Tokens{}, errors.New("codexauth: refresh response has no tokens")
	}
	return mergeTokens(current, payload, time.Now())
}

func (c *Client) poll(ctx context.Context, code DeviceCode) (codeResponse, bool, error) {
	body, err := json.Marshal(tokenPollRequest{
		DeviceAuthID: code.deviceAuthID,
		UserCode:     code.UserCode,
	})
	if err != nil {
		return codeResponse{}, false, fmt.Errorf("codexauth: encode token poll request: %w", err)
	}
	resp, cancel, err := c.post(ctx, c.endpoint("/api/accounts/deviceauth/token"), "application/json", body)
	if err != nil {
		return codeResponse{}, false, fmt.Errorf("codexauth: poll device login: %w", err)
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return codeResponse{}, true, nil
	}
	if !successfulStatus(resp.StatusCode) {
		return codeResponse{}, false, &HTTPError{Operation: "poll device login", StatusCode: resp.StatusCode}
	}
	var payload codeResponse
	if err := decodeResponse(resp.Body, &payload); err != nil {
		return codeResponse{}, false, fmt.Errorf("codexauth: decode device login response: %w", err)
	}
	if payload.AuthorizationCode == "" || payload.CodeChallenge == "" || payload.CodeVerifier == "" {
		return codeResponse{}, false, errors.New("codexauth: device login response is incomplete")
	}
	return payload, false, nil
}

func (c *Client) exchange(ctx context.Context, code codeResponse) (Tokens, error) {
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code.AuthorizationCode},
		"redirect_uri":  {c.endpoint("/deviceauth/callback")},
		"client_id":     {c.clientID},
		"code_verifier": {code.CodeVerifier},
	}
	resp, cancel, err := c.post(
		ctx,
		c.endpoint("/oauth/token"),
		"application/x-www-form-urlencoded",
		[]byte(values.Encode()),
	)
	if err != nil {
		return Tokens{}, fmt.Errorf("codexauth: exchange authorization code: %w", err)
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if !successfulStatus(resp.StatusCode) {
		return Tokens{}, &HTTPError{Operation: "exchange authorization code", StatusCode: resp.StatusCode}
	}
	var payload tokenResponse
	if err := decodeResponse(resp.Body, &payload); err != nil {
		return Tokens{}, fmt.Errorf("codexauth: decode token response: %w", err)
	}
	return mergeTokens(Tokens{}, payload, time.Now())
}

func mergeTokens(current Tokens, payload tokenResponse, now time.Time) (Tokens, error) {
	merged := current
	if payload.IDToken != "" {
		merged.IDToken = payload.IDToken
		accountID, err := accountIDFromJWT(payload.IDToken)
		if err != nil {
			return Tokens{}, fmt.Errorf("codexauth: decode id token: %w", err)
		}
		if accountID != "" {
			merged.AccountID = accountID
		}
	}
	if payload.AccessToken != "" {
		merged.AccessToken = payload.AccessToken
		merged.ExpiresAt = time.Time{}
		if expiresAt, ok := expirationFromJWT(payload.AccessToken); ok {
			merged.ExpiresAt = expiresAt
		} else if payload.ExpiresIn > 0 {
			merged.ExpiresAt = now.Add(time.Duration(payload.ExpiresIn) * time.Second)
		}
	}
	if payload.RefreshToken != "" {
		merged.RefreshToken = payload.RefreshToken
	}
	if merged.IDToken == "" {
		return Tokens{}, errors.New("codexauth: token response has no id token")
	}
	if merged.AccessToken == "" {
		return Tokens{}, errors.New("codexauth: token response has no access token")
	}
	if merged.RefreshToken == "" {
		return Tokens{}, errors.New("codexauth: token response has no refresh token")
	}
	if merged.AccountID == "" {
		accountID, err := accountIDFromJWT(merged.IDToken)
		if err != nil {
			return Tokens{}, fmt.Errorf("codexauth: decode id token: %w", err)
		}
		merged.AccountID = accountID
	}
	return merged, nil
}

func accountIDFromJWT(token string) (string, error) {
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return "", err
	}
	if accountID, ok := claims["chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID, nil
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if accountID, ok := auth["chatgpt_account_id"].(string); ok && accountID != "" {
			return accountID, nil
		}
	}
	return "", nil
}

func expirationFromJWT(token string) (time.Time, bool) {
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return time.Time{}, false
	}
	expires, ok := claims["exp"].(json.Number)
	if !ok {
		return time.Time{}, false
	}
	seconds, err := expires.Int64()
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != jwtPartCount || parts[0] == "" || parts[jwtPayloadPart] == "" || parts[jwtSignaturePart] == "" {
		return nil, errors.New("token is not a jwt")
	}
	if len(parts[jwtPayloadPart]) > base64.RawURLEncoding.EncodedLen(maxJWTClaimsBytes) {
		return nil, errors.New("jwt claims are too large")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[jwtPayloadPart])
	if err != nil {
		return nil, fmt.Errorf("decode jwt claims: %w", err)
	}
	if len(payload) > maxJWTClaimsBytes {
		return nil, errors.New("jwt claims are too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var claims map[string]any
	if err := decoder.Decode(&claims); err != nil {
		return nil, fmt.Errorf("decode jwt claims: %w", err)
	}
	if claims == nil {
		return nil, errors.New("decode jwt claims: claims are not an object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode jwt claims: trailing data")
	}
	return claims, nil
}

func (c *Client) post(ctx context.Context, endpoint, contentType string, body []byte) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(nonNilContext(ctx), c.requestTimeout)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(request)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return resp, cancel, nil
}

func (c *Client) endpoint(path string) string {
	return c.issuer + path
}

func decodeResponse(body io.Reader, output any) error {
	encoded, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(encoded) > maxResponseBytes {
		return fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		return err
	}
	return nil
}

func waitForPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

func secureHTTPClient(base *http.Client, issuer *url.URL) *http.Client {
	secured := *base
	callerPolicy := secured.CheckRedirect
	secured.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= defaultMaxRedirects {
			return errors.New("codexauth: stopped after too many redirects")
		}
		if !sameOrigin(issuer, request.URL) {
			return errors.New("codexauth: refusing cross-origin redirect")
		}
		if callerPolicy != nil {
			return callerPolicy(request, via)
		}
		return nil
	}
	return &secured
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.TODO()
	}
	return ctx
}

func successfulStatus(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

type seconds time.Duration

func (s *seconds) UnmarshalJSON(encoded []byte) error {
	value := strings.TrimSpace(string(encoded))
	if len(value) >= quotedValueMinBytes && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return err
		}
		value = unquoted
	}
	amount, err := strconv.ParseInt(value, decimalBase, signedIntBits)
	if err != nil {
		return err
	}
	if amount < 0 || amount > int64(maxPollInterval/time.Second) {
		return errors.New("poll interval is invalid")
	}
	*s = seconds(time.Duration(amount) * time.Second)
	return nil
}

type userCodeRequest struct {
	ClientID string `json:"client_id"`
}

type userCodeResponse struct {
	DeviceAuthID   string  `json:"device_auth_id"`
	UserCode       string  `json:"user_code"`
	LegacyUserCode string  `json:"usercode"`
	Interval       seconds `json:"interval"`
}

type tokenPollRequest struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
}

type codeResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

type refreshRequest struct {
	ClientID     string `json:"client_id"`
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
}

type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}
