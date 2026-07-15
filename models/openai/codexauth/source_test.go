package codexauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewSourceValidatesConfiguration(t *testing.T) {
	client, err := New()
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryTokenStore{}
	if _, err := NewSource(nil, store); err == nil {
		t.Fatal("NewSource(nil) error = nil")
	}
	if _, err := NewSource(client, nil); err == nil {
		t.Fatal("NewSource(nil store) error = nil")
	}
	if _, err := NewSource(client, store, WithRefreshWindow(-time.Second)); err == nil {
		t.Fatal("NewSource(negative window) error = nil")
	}
}

func TestSourceTokenRefreshesAndPersistsExpiringTokens(t *testing.T) {
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode refresh request: %v", err)
			return
		}
		if request.RefreshToken != "old-refresh" {
			t.Errorf("refresh token = %q", request.RefreshToken)
		}
		refreshes.Add(1)
		_ = json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
			ExpiresIn:    3600,
		})
	}))
	defer server.Close()
	client, err := New(WithIssuer(server.URL), WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryTokenStore{tokens: Tokens{
		IDToken:      "id-token",
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		AccountID:    "account",
		ExpiresAt:    time.Now().Add(time.Minute),
	}}
	source, err := NewSource(client, store, WithRefreshWindow(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	tokens, err := source.Token(t.Context())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tokens.AccessToken != "new-access" || tokens.RefreshToken != "new-refresh" || tokens.AccountID != "account" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if refreshes.Load() != 1 || store.saves.Load() != 1 {
		t.Fatalf("refreshes/saves = %d/%d", refreshes.Load(), store.saves.Load())
	}

	if _, err := source.Token(t.Context()); err != nil {
		t.Fatalf("second Token() error = %v", err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, want one", refreshes.Load())
	}
}

func TestSourceRefreshForcesRotation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "forced", ExpiresIn: 3600})
	}))
	defer server.Close()
	client, err := New(WithIssuer(server.URL), WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryTokenStore{tokens: Tokens{
		IDToken:      "id-token",
		AccessToken:  "current",
		RefreshToken: "refresh",
		AccountID:    "account",
		ExpiresAt:    time.Now().Add(time.Hour),
	}}
	source, err := NewSource(client, store)
	if err != nil {
		t.Fatal(err)
	}

	tokens, err := source.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if tokens.AccessToken != "forced" || store.saves.Load() != 1 {
		t.Fatalf("tokens/saves = %+v/%d", tokens, store.saves.Load())
	}
}

func TestSourceSerializesConcurrentRefresh(t *testing.T) {
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshes.Add(1)
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "fresh", RefreshToken: "rotated", ExpiresIn: 3600})
	}))
	defer server.Close()
	client, err := New(WithIssuer(server.URL), WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryTokenStore{tokens: Tokens{
		IDToken:      "id-token",
		AccessToken:  "expired",
		RefreshToken: "refresh",
		AccountID:    "account",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}}
	source, err := NewSource(client, store)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			tokens, err := source.Token(t.Context())
			if err != nil {
				t.Errorf("Token() error = %v", err)
			} else if tokens.AccessToken != "fresh" {
				t.Errorf("access token = %q", tokens.AccessToken)
			}
		}()
	}
	wait.Wait()
	if refreshes.Load() != 1 || store.saves.Load() != 1 {
		t.Fatalf("refreshes/saves = %d/%d", refreshes.Load(), store.saves.Load())
	}
}

func TestSourceReturnsStoreErrors(t *testing.T) {
	client, err := New()
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("load failed")
	source, err := NewSource(client, errorTokenStore{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Token() error = %v, want %v", err, want)
	}
}

type memoryTokenStore struct {
	mu     sync.Mutex
	tokens Tokens
	saves  atomic.Int32
}

func (store *memoryTokenStore) Load(context.Context) (Tokens, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.tokens, nil
}

func (store *memoryTokenStore) Save(_ context.Context, tokens Tokens) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tokens = tokens
	store.saves.Add(1)
	return nil
}

type errorTokenStore struct{ err error }

func (store errorTokenStore) Load(context.Context) (Tokens, error) { return Tokens{}, store.err }
func (errorTokenStore) Save(context.Context, Tokens) error         { return nil }
