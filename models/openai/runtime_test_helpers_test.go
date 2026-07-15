package openai

import (
	"net/http"
	"testing"
)

func dispatchRuntimeTestRoute(t *testing.T, routes map[string]http.HandlerFunc, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	handler, ok := routes[r.Method+" "+r.URL.Path]
	if !ok {
		http.Error(w, "unexpected request", http.StatusNotFound)
		return
	}
	handler(w, r)
}
