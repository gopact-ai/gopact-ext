package codex

import (
	"net/http"
	"testing"
)

func dispatchAccountTestRoute(t *testing.T, routes map[string]http.HandlerFunc, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	handler, ok := routes[r.Method+" "+r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	handler(w, r)
}
