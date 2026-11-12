// Package fakeapi is an HTTP API on loopback for tests. A route pairs a request target (path
// and query) with its JSON answer, and every request it gets is kept.
package fakeapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Seen is one request the API got.
type Seen struct {
	Method string
	Target string
	Auth   string
	Body   string
}

// Api is the server. Origin is its scheme://host:port, and a {base} in an answer becomes it.
type Api struct {
	Origin string

	mu     sync.Mutex
	routes map[string]any
	seen   []Seen
}

// Serve starts an API that stops with the test.
func Serve(t *testing.T) *Api {
	t.Helper()
	api := &Api{routes: map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(server.Close)
	api.Origin = server.URL
	return api
}

func (a *Api) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	answer, ok := a.routes[r.URL.RequestURI()]
	a.seen = append(a.seen, Seen{Method: r.Method, Target: r.URL.RequestURI(), Auth: r.Header.Get("Authorization"), Body: string(body)})
	a.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	out, err := json.Marshal(answer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(strings.ReplaceAll(string(out), "{base}", a.Origin)))
}

// Route adds a route, or replaces the answer to one that exists.
func (a *Api) Route(target string, answer any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routes[target] = answer
}

// Posts are the requests that changed something, oldest first.
func (a *Api) Posts() []Seen {
	a.mu.Lock()
	defer a.mu.Unlock()
	var posts []Seen
	for _, s := range a.seen {
		if s.Method == http.MethodPost {
			posts = append(posts, s)
		}
	}
	return posts
}

// Targets are every request's target, oldest first.
func (a *Api) Targets() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var targets []string
	for _, s := range a.seen {
		targets = append(targets, s.Target)
	}
	return targets
}
