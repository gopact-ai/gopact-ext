package codexauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const defaultRefreshWindow = 5 * time.Minute

// Store loads and atomically replaces Codex OAuth credentials. Implementations
// must protect the complete Tokens value as a secret and be safe for concurrent
// use when shared by multiple Sources.
type Store interface {
	Load(context.Context) (Tokens, error)
	Save(context.Context, Tokens) error
}

// Source loads credentials from a Store and refreshes them before expiry. It
// also exposes Refresh so a model provider can force one rotation after an
// unauthorized response. A Source serializes refreshes within one process.
type Source struct {
	client        *Client
	store         Store
	refreshWindow time.Duration
	configErr     error
	mu            sync.Mutex
}

// SourceOption configures a Source.
type SourceOption func(*Source)

// WithRefreshWindow sets how early Token refreshes expiring access tokens. A
// zero window refreshes only tokens whose expiry has been reached.
func WithRefreshWindow(window time.Duration) SourceOption {
	return func(source *Source) {
		if window < 0 {
			source.configErr = errors.Join(source.configErr, errors.New("codexauth: refresh window must not be negative"))
			return
		}
		source.refreshWindow = window
	}
}

// NewSource creates a persisted, refreshing token source.
func NewSource(client *Client, store Store, opts ...SourceOption) (*Source, error) {
	source := &Source{
		client:        client,
		store:         store,
		refreshWindow: defaultRefreshWindow,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(source)
		}
	}
	if source.configErr != nil {
		return nil, source.configErr
	}
	if source.client == nil {
		return nil, errors.New("codexauth: source client is nil")
	}
	if source.store == nil {
		return nil, errors.New("codexauth: token store is nil")
	}
	return source, nil
}

// Token returns current credentials, refreshing and persisting them first when
// the access token is close to expiry.
func (source *Source) Token(ctx context.Context) (Tokens, error) {
	if source == nil {
		return Tokens{}, errors.New("codexauth: source is nil")
	}
	source.mu.Lock()
	defer source.mu.Unlock()

	ctx = nonNilContext(ctx)
	tokens, err := source.load(ctx)
	if err != nil {
		return Tokens{}, err
	}
	if tokens.ExpiresAt.IsZero() || time.Until(tokens.ExpiresAt) > source.refreshWindow {
		return tokens, nil
	}
	return source.refresh(ctx, tokens)
}

// Refresh forces one credential rotation and persists the complete replacement
// before returning it.
func (source *Source) Refresh(ctx context.Context) (Tokens, error) {
	if source == nil {
		return Tokens{}, errors.New("codexauth: source is nil")
	}
	source.mu.Lock()
	defer source.mu.Unlock()

	ctx = nonNilContext(ctx)
	tokens, err := source.load(ctx)
	if err != nil {
		return Tokens{}, err
	}
	return source.refresh(ctx, tokens)
}

func (source *Source) load(ctx context.Context) (Tokens, error) {
	tokens, err := source.store.Load(ctx)
	if err != nil {
		return Tokens{}, fmt.Errorf("codexauth: load tokens: %w", err)
	}
	if tokens.AccessToken == "" {
		return Tokens{}, errors.New("codexauth: stored access token is empty")
	}
	return tokens, nil
}

func (source *Source) refresh(ctx context.Context, current Tokens) (Tokens, error) {
	replacement, err := source.client.Refresh(ctx, current)
	if err != nil {
		return Tokens{}, err
	}
	if err := source.store.Save(ctx, replacement); err != nil {
		return Tokens{}, fmt.Errorf("codexauth: save refreshed tokens: %w", err)
	}
	return replacement, nil
}
