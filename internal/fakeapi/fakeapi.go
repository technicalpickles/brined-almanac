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

	mu       sync.Mutex
	routes   map[string]any
	statuses map[string]int
	headers  map[string]map[string]string
	seen     []Seen
}

// Serve starts an API that stops with the test.
func Serve(t *testing.T) *Api {
	t.Helper()
	api := &Api{routes: map[string]any{}, statuses: map[string]int{}, headers: map[string]map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(server.Close)
	api.Origin = server.URL
	return api
}

func (a *Api) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	answer, ok := a.routes[r.URL.RequestURI()]
	status, hasStatus := a.statuses[r.URL.RequestURI()]
	headers := a.headers[r.URL.RequestURI()]
	a.seen = append(a.seen, Seen{Method: r.Method, Target: r.URL.RequestURI(), Auth: r.Header.Get("Authorization"), Body: string(body)})
	a.mu.Unlock()
	if hasStatus {
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		return
	}
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

// Status answers a request target with just a status and an empty body, as GitHub answers 204
// for a switch that is on. It wins over a route for the same target.
func (a *Api) Status(target string, status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.statuses[target] = status
}

// StatusWith is Status with response headers, such as the Retry-After of a rate limit.
func (a *Api) StatusWith(target string, status int, headers map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.statuses[target] = status
	a.headers[target] = headers
}

// Count is how many requests got target, query included.
func (a *Api) Count(target string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, s := range a.seen {
		if s.Target == target {
			n++
		}
	}
	return n
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

// Requests are every request the API got, oldest first.
func (a *Api) Requests() []Seen {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Seen(nil), a.seen...)
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
