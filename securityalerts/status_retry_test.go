package securityalerts_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivy/almanac/diag"
	"github.com/ivy/almanac/github"

	"github.com/technicalpickles/brined-almanac/internal/fakeapi"
	"github.com/technicalpickles/brined-almanac/securityalerts"
)

const switchPath = "/repos/o/r/vulnerability-alerts"

// answer is one scripted reply of a flaky GitHub.
type answer struct {
	status  int
	headers map[string]string
}

// scripted serves its answers in order and keeps the last one for any request after them.
func scripted(t *testing.T, answers ...answer) (origin string, count func() int) {
	t.Helper()
	var mu sync.Mutex
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		a := answers[min(served, len(answers)-1)]
		served++
		mu.Unlock()
		for k, v := range a.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(a.status)
	}))
	t.Cleanup(server.Close)
	return server.URL, func() int { mu.Lock(); defer mu.Unlock(); return served }
}

func quick(origin string, retry github.Retry) *securityalerts.Client {
	c := securityalerts.NewClient(origin, token)
	c.Retry = retry
	return c
}

func TestStatusTriesAgainAfterAServerErrorAndBacksOff(t *testing.T) {
	origin, count := scripted(t, answer{status: 502}, answer{status: 503}, answer{status: 500}, answer{status: 204})
	c := quick(origin, github.Retry{Attempts: 5, Backoff: 30 * time.Millisecond, LongestWait: time.Minute})

	start := time.Now()
	err := c.Status(context.Background(), switchPath)
	took := time.Since(start)

	if err != nil {
		t.Fatalf("a switch that answers after three server errors is on: %v", err)
	}
	if count() != 4 {
		t.Errorf("requests %d", count())
	}
	if min := (30 + 60 + 120) * time.Millisecond; took < min {
		t.Errorf("waits should double from the first backoff, so at least %v; took %v", min, took)
	}
}

func TestStatusGivesUpAfterTheLastAttemptWithoutCallingItARefusal(t *testing.T) {
	api := fakeapi.Serve(t)
	api.Status(switchPath, 502)
	c := quick(api.Origin, github.Retry{Attempts: 3, Backoff: time.Millisecond, LongestWait: time.Minute})

	err := c.Status(context.Background(), switchPath)

	var refusal *github.Refusal
	if err == nil || errors.As(err, &refusal) {
		t.Fatalf("a server that never recovers is a failure, not a refusal: %v", err)
	}
	if got := len(api.Targets()); got != 3 {
		t.Errorf("requests %d", got)
	}
}

func TestStatusReportsAnUnreachableGithubAsAFailure(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadOrigin := dead.URL
	dead.Close()
	broken := quick(deadOrigin, github.Retry{Attempts: 3, Backoff: time.Millisecond})

	var refusal *github.Refusal
	if err := broken.Status(context.Background(), switchPath); err == nil || errors.As(err, &refusal) {
		t.Errorf("no answer at all is a failure, not a refusal: %v", err)
	}
}

func TestStatusReportsARefusalAtOnce(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			origin, count := scripted(t, answer{status: status}, answer{status: 204})
			c := quick(origin, github.Retry{Attempts: 5, Backoff: time.Millisecond, LongestWait: time.Minute})

			err := c.Status(context.Background(), switchPath)

			var refusal *github.Refusal
			if !errors.As(err, &refusal) || refusal.Status != status {
				t.Errorf("%v", err)
			}
			if count() != 1 {
				t.Errorf("a refusal is not retried, but %d requests were made", count())
			}
		})
	}
}

func TestStatusWaitsOutARateLimitAsAsked(t *testing.T) {
	limited := map[string]answer{
		"429 with Retry-After":        {status: 429, headers: map[string]string{"Retry-After": "1"}},
		"403 with Retry-After":        {status: 403, headers: map[string]string{"Retry-After": "1"}},
		"403 with the quota used up":  {status: 403, headers: map[string]string{"X-RateLimit-Remaining": "0", "Retry-After": "1"}},
		"403 that resets in a second": {status: 403, headers: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10)}},
	}
	for name, first := range limited {
		t.Run(name, func(t *testing.T) {
			origin, count := scripted(t, first, answer{status: 204})
			c := quick(origin, github.Retry{Attempts: 5, Backoff: time.Millisecond, LongestWait: time.Minute})

			start := time.Now()
			err := c.Status(context.Background(), switchPath)

			if err != nil {
				t.Fatalf("after the wait the switch answers on: %v", err)
			}
			if count() != 2 {
				t.Errorf("requests %d", count())
			}
			if took := time.Since(start); took < 900*time.Millisecond {
				t.Errorf("the rate limit asked for a second and Status did not wait for it: %v", took)
			}
		})
	}
}

func TestStatusReportsARateLimitThatAsksForTooLong(t *testing.T) {
	origin, count := scripted(t, answer{status: 429, headers: map[string]string{"Retry-After": "120"}}, answer{status: 204})
	c := quick(origin, github.Retry{Attempts: 5, Backoff: time.Millisecond, LongestWait: time.Minute})

	err := c.Status(context.Background(), switchPath)

	var reported *diag.Error
	if !errors.As(err, &reported) || reported.Code != diag.ForgeRateLimited {
		t.Fatalf("%v", err)
	}
	if count() != 1 {
		t.Errorf("a wait longer than the longest is not waited out, but %d requests were made", count())
	}
}

func TestStatusReportsARateLimitOnTheLastAttempt(t *testing.T) {
	origin, count := scripted(t, answer{status: 429, headers: map[string]string{"Retry-After": "0"}})
	c := quick(origin, github.Retry{Attempts: 3, Backoff: time.Millisecond, LongestWait: time.Minute})

	err := c.Status(context.Background(), switchPath)

	var reported *diag.Error
	if !errors.As(err, &reported) || reported.Code != diag.ForgeRateLimited {
		t.Fatalf("%v", err)
	}
	if count() != 3 {
		t.Errorf("requests %d", count())
	}
}

func TestStatusStopsWaitingWhenTheContextEnds(t *testing.T) {
	origin, _ := scripted(t, answer{status: 429, headers: map[string]string{"Retry-After": "30"}})
	c := quick(origin, github.Retry{Attempts: 5, Backoff: time.Millisecond, LongestWait: time.Minute})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.Status(ctx, switchPath)

	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("%v after %v", err, time.Since(start))
	}
}

func TestAFlakySwitchStillReadsTheRepository(t *testing.T) {
	server := httptest.NewServer(&flakyFirst{next: repoApi(t)})
	t.Cleanup(server.Close)
	c := securityalerts.NewClient(server.URL, token)
	c.Retry = github.Retry{Attempts: 3, Backoff: time.Millisecond}

	got, err := securityalerts.ReadRepo(context.Background(), c, "o/r")

	if err != nil {
		t.Fatalf("one 502 on the switch should not fail the repository: %v", err)
	}
	if !got.Dependabot.On || len(got.Dependabot.Alerts) != 1 {
		t.Errorf("%+v", got)
	}
}

// flakyFirst answers the switch with a 502 the first time and otherwise hands over to next.
type flakyFirst struct {
	mu   sync.Mutex
	hit  bool
	next *fakeapi.Api
}

func (f *flakyFirst) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	failing := r.URL.Path == switchPath && !f.hit
	if failing {
		f.hit = true
	}
	f.mu.Unlock()
	if failing {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	proxy, _ := http.NewRequestWithContext(r.Context(), r.Method, f.next.Origin+r.URL.RequestURI(), nil)
	response, err := http.DefaultClient.Do(proxy)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for k, v := range response.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(response.StatusCode)
	buf := make([]byte, 32*1024)
	for {
		n, err := response.Body.Read(buf)
		_, _ = w.Write(buf[:n])
		if err != nil {
			return
		}
	}
}

func TestStatusTriesAgainAfterAConnectionDropsMidAnswer(t *testing.T) {
	var mu sync.Mutex
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		served++
		first := served == 1
		mu.Unlock()
		if first {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	c := quick(server.URL, github.Retry{Attempts: 3, Backoff: time.Millisecond})

	if err := c.Status(context.Background(), switchPath); err != nil {
		t.Fatalf("a dropped connection is tried again: %v", err)
	}
}
