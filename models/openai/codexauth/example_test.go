package codexauth_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

func ExampleClient() {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			body = `{"device_auth_id":"device-123","user_code":"ABCD-EFGH","interval":"1"}`
		case "/api/accounts/deviceauth/token":
			body = `{"authorization_code":"authorization-123","code_challenge":"challenge-123","code_verifier":"verifier-123"}`
		case "/oauth/token":
			body = exampleTokenBody()
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       http.NoBody,
				Header:     make(http.Header),
				Request:    request,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    request,
		}, nil
	})
	client, err := codexauth.New(
		codexauth.WithIssuer("https://auth.example.test"),
		codexauth.WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err != nil {
		panic(err)
	}

	code, err := client.Start(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Println("Open", code.VerificationURL)
	fmt.Println("Enter", code.UserCode)

	tokens, err := client.Wait(context.Background(), code)
	if err != nil {
		panic(err)
	}
	fmt.Println("Signed in as", tokens.AccountID)

	// Output:
	// Open https://auth.example.test/codex/device
	// Enter ABCD-EFGH
	// Signed in as account-123
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func exampleJWT(payload string) string {
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + encode([]byte(payload)) + ".signature"
}

func exampleTokenBody() string {
	idToken := exampleJWT(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account-123"}}`)
	accessToken := exampleJWT(`{"exp":4102444800}`)
	return fmt.Sprintf(
		`{"id_token":%q,"access_token":%q,"refresh_token":"refresh-123"}`,
		idToken,
		accessToken,
	)
}
