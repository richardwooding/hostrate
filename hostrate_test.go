package hostrate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// stubTransport is a base RoundTripper that records the host of each request and
// returns a canned 200 response.
type stubTransport struct {
	mu    sync.Mutex
	hosts []string
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.hosts = append(s.hosts, req.URL.Hostname())
	s.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    req,
	}, nil
}

func (s *stubTransport) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.hosts)
}

func mustRequest(t *testing.T, ctx context.Context, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func TestRoundTripDelegatesToBase(t *testing.T) {
	stub := &stubTransport{}
	tr := New(stub, rate.Limit(100), 5)

	resp, err := tr.RoundTrip(mustRequest(t, context.Background(), "https://example.com/x"))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if stub.count() != 1 {
		t.Fatalf("base called %d times, want 1", stub.count())
	}
}

func TestPerHostIsolation(t *testing.T) {
	stub := &stubTransport{}
	// One token per host, refilling only once per hour.
	tr := New(stub, rate.Every(time.Hour), 1)

	// First request to host A consumes its single token.
	if _, err := tr.RoundTrip(mustRequest(t, context.Background(), "https://a.example/1")); err != nil {
		t.Fatalf("A first request: %v", err)
	}

	// Second request to host A must be throttled (bucket empty). With a short
	// deadline, Wait refuses rather than passing through; the base-call count
	// below confirms it never reached the underlying transport.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := tr.RoundTrip(mustRequest(t, ctx, "https://a.example/2")); err == nil {
		t.Fatal("A second request should be throttled, got nil error")
	}

	// Host B has its own independent bucket and should pass immediately.
	if _, err := tr.RoundTrip(mustRequest(t, context.Background(), "https://b.example/1")); err != nil {
		t.Fatalf("B first request: %v", err)
	}

	if got, want := stub.count(), 2; got != want {
		t.Fatalf("base called %d times, want %d (A1 + B1; A2 throttled)", got, want)
	}
	if got := tr.Len(); got != 2 {
		t.Fatalf("tracked hosts = %d, want 2", got)
	}
}

func TestBurst(t *testing.T) {
	stub := &stubTransport{}
	tr := New(stub, rate.Every(time.Hour), 3)

	for i := range 3 {
		if _, err := tr.RoundTrip(mustRequest(t, context.Background(), "https://a.example/x")); err != nil {
			t.Fatalf("burst request %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := tr.RoundTrip(mustRequest(t, ctx, "https://a.example/x")); err == nil {
		t.Fatal("post-burst request should be throttled, got nil error")
	}
	if stub.count() != 3 {
		t.Fatalf("base called %d times, want 3", stub.count())
	}
}

func TestContextCancellationDoesNotCallBase(t *testing.T) {
	stub := &stubTransport{}
	tr := New(stub, rate.Every(time.Hour), 1)

	// Drain the single token.
	if _, err := tr.RoundTrip(mustRequest(t, context.Background(), "https://a.example/1")); err != nil {
		t.Fatalf("first request: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled
	_, err := tr.RoundTrip(mustRequest(t, ctx, "https://a.example/2"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if stub.count() != 1 {
		t.Fatalf("base called %d times, want 1 (throttled request must not reach base)", stub.count())
	}
}

func TestWithKeyFuncGroupsRequests(t *testing.T) {
	stub := &stubTransport{}
	// Constant key: every host shares one bucket.
	tr := New(stub, rate.Limit(100), 5, WithKeyFunc(func(*http.Request) string { return "shared" }))

	for _, host := range []string{"https://a.example", "https://b.example", "https://c.example"} {
		if _, err := tr.RoundTrip(mustRequest(t, context.Background(), host)); err != nil {
			t.Fatalf("request to %s: %v", host, err)
		}
	}
	if got := tr.Len(); got != 1 {
		t.Fatalf("tracked keys = %d, want 1 (all share one key)", got)
	}
}

func TestKeyByHostPort(t *testing.T) {
	a := mustRequest(t, context.Background(), "https://example.com:8443/x")
	b := mustRequest(t, context.Background(), "https://example.com:9443/x")
	if KeyByHostPort(a) == KeyByHostPort(b) {
		t.Fatal("KeyByHostPort should distinguish different ports")
	}
	if KeyByHost(a) != KeyByHost(b) {
		t.Fatal("KeyByHost should ignore port")
	}
}

func TestIdleEviction(t *testing.T) {
	stub := &stubTransport{}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr := New(stub, rate.Inf, 1, WithIdleTimeout(10*time.Minute))
	tr.now = func() time.Time { return clock }
	tr.lastSweep = clock

	tr.limiterFor("a")
	if tr.Len() != 1 {
		t.Fatalf("after touching a: Len = %d, want 1", tr.Len())
	}

	// Advance past the idle window, then touch a different key to trigger a sweep.
	clock = clock.Add(11 * time.Minute)
	tr.limiterFor("b")

	if got := tr.Len(); got != 1 {
		t.Fatalf("after eviction: Len = %d, want 1 (a evicted, b present)", got)
	}
	if _, ok := tr.entries["a"]; ok {
		t.Fatal("idle key a should have been evicted")
	}
	if _, ok := tr.entries["b"]; !ok {
		t.Fatal("recently used key b should be present")
	}
}

func TestNoEvictionByDefault(t *testing.T) {
	stub := &stubTransport{}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr := New(stub, rate.Inf, 1) // no idle timeout
	tr.now = func() time.Time { return clock }

	tr.limiterFor("a")
	clock = clock.Add(100 * time.Hour)
	tr.limiterFor("b")

	if got := tr.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2 (eviction disabled by default)", got)
	}
}

func TestSameLimiterReturnedForKey(t *testing.T) {
	tr := New(nil, rate.Limit(1), 1)
	first := tr.limiterFor("a")
	second := tr.limiterFor("a")
	if first != second {
		t.Fatal("limiterFor returned different limiters for the same key")
	}
}

func TestNilBaseUsesDefaultTransport(t *testing.T) {
	tr := New(nil, rate.Limit(1), 1)
	if tr.base != http.DefaultTransport {
		t.Fatal("nil base should default to http.DefaultTransport")
	}
}

func TestOptionsIgnoreInvalidValues(t *testing.T) {
	tr := New(nil, rate.Limit(1), 1, WithKeyFunc(nil), WithIdleTimeout(-time.Second))
	if tr.keyFunc == nil {
		t.Fatal("nil KeyFunc should be ignored, leaving the default")
	}
	if tr.idle != 0 {
		t.Fatalf("non-positive idle timeout should be ignored, got %v", tr.idle)
	}
}

func TestNewClientHasRateLimitedTransport(t *testing.T) {
	client := NewClient(2, 5)
	if _, ok := client.Transport.(*Transport); !ok {
		t.Fatalf("NewClient transport = %T, want *hostrate.Transport", client.Transport)
	}
}
