package codex_test

import (
	"context"
	"fmt"
	"sync"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai/codex"
	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

func ExampleNew() {
	auth, err := codexauth.New()
	if err != nil {
		panic(err)
	}
	store := &exampleTokenStore{tokens: codexauth.Tokens{
		AccessToken:  "secret-access-token",
		RefreshToken: "secret-refresh-token",
		AccountID:    "account-123",
	}}
	source, err := codexauth.NewSource(auth, store)
	if err != nil {
		panic(err)
	}
	model, err := codex.New("gpt-5.4", source)
	if err != nil {
		panic(err)
	}

	request := model.NewRequest(gopact.UserMessage("Explain this repository."))
	fmt.Println(request.Model, request.Messages[0].Role)

	// Output: gpt-5.4 user
}

type exampleTokenStore struct {
	mu     sync.Mutex
	tokens codexauth.Tokens
}

func (store *exampleTokenStore) Load(context.Context) (codexauth.Tokens, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.tokens, nil
}

func (store *exampleTokenStore) Save(_ context.Context, tokens codexauth.Tokens) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tokens = tokens
	return nil
}
