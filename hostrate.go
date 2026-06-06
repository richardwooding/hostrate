// Package hostrate provides an [http.RoundTripper] that rate-limits outbound
// HTTP requests on a per-host basis (or by any custom key), so a client stays
// polite to each remote server independently rather than sharing one global
// budget across all hosts.
//
// Each distinct key gets its own [golang.org/x/time/rate.Limiter], created
// lazily on first use. The default key is the request's host, so traffic to
// api.a.com is limited separately from api.b.com.
//
// Basic use:
//
//	client := hostrate.NewClient(2, 5) // 2 req/s, burst 5, per host
//	resp, err := client.Get("https://example.com/feed.xml")
//
// Composing with a custom base transport (for example, tuned connection
// pooling) and keying:
//
//	t := hostrate.New(myTransport, rate.Limit(2), 5,
//		hostrate.WithKeyFunc(hostrate.KeyByHostPort),
//		hostrate.WithIdleTimeout(10*time.Minute), // bound memory for unbounded hosts
//	)
//	client := &http.Client{Transport: t}
//
// A Transport blocks each request until its key's limiter permits the call or
// the request's context is done, so request deadlines and cancellation are
// honored while waiting.
package hostrate

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// KeyFunc derives the rate-limiting key for a request. Requests that map to the
// same key share a single token-bucket limiter.
type KeyFunc func(req *http.Request) string

// KeyByHost keys requests by their lowercased host without port. It is the
// default [KeyFunc].
func KeyByHost(req *http.Request) string {
	return strings.ToLower(req.URL.Hostname())
}

// KeyByHostPort keys requests by their lowercased host including port, so the
// same hostname on different ports is limited independently.
func KeyByHostPort(req *http.Request) string {
	return strings.ToLower(req.URL.Host)
}

// hostEntry holds a key's limiter and the last time it was used (for eviction).
type hostEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// Transport is an [http.RoundTripper] that applies a per-key token-bucket rate
// limit before delegating to a base RoundTripper. A separate limiter is created
// lazily for each distinct key (by default, each target host).
//
// The zero value is not usable; construct a Transport with [New]. A Transport
// is safe for concurrent use by multiple goroutines.
type Transport struct {
	base    http.RoundTripper
	limit   rate.Limit
	burst   int
	keyFunc KeyFunc
	idle    time.Duration

	mu        sync.Mutex
	entries   map[string]*hostEntry
	lastSweep time.Time
	now       func() time.Time // overridable clock, for tests
}

// Option configures a [Transport] passed to [New].
type Option func(*Transport)

// WithKeyFunc sets the function used to derive each request's limiter key. The
// default is [KeyByHost]. A nil KeyFunc is ignored.
func WithKeyFunc(fn KeyFunc) Option {
	return func(t *Transport) {
		if fn != nil {
			t.keyFunc = fn
		}
	}
}

// WithIdleTimeout enables eviction of per-key limiters that have not been used
// for at least d. This bounds memory when the set of keys is effectively
// unbounded (for example, a crawler reaching arbitrarily many hosts).
//
// The default is zero, which disables eviction and retains one limiter per key
// for the Transport's lifetime — appropriate when the set of hosts is small and
// known. A non-positive d disables eviction.
func WithIdleTimeout(d time.Duration) Option {
	return func(t *Transport) {
		if d > 0 {
			t.idle = d
		}
	}
}

// New returns a [Transport] that limits requests sharing a key to limit
// requests per second with the given burst, delegating to base. If base is nil,
// [http.DefaultTransport] is used.
//
// limit is a [golang.org/x/time/rate.Limit]: use rate.Limit(n) for n requests
// per second, [rate.Every](d) for one request per d, or [rate.Inf] to disable
// limiting. burst is the maximum number of requests allowed to proceed at once
// before throttling begins.
func New(base http.RoundTripper, limit rate.Limit, burst int, opts ...Option) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	t := &Transport{
		base:    base,
		limit:   limit,
		burst:   burst,
		keyFunc: KeyByHost,
		entries: make(map[string]*hostEntry),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(t)
	}
	t.lastSweep = t.now()
	return t
}

// NewClient is a convenience constructor returning an [http.Client] whose
// Transport rate-limits to rps requests per second per host (with the given
// burst), wrapping [http.DefaultTransport]. For a custom base transport — for
// example, tuned connection pooling — build a [Transport] with [New] instead.
func NewClient(rps float64, burst int, opts ...Option) *http.Client {
	return &http.Client{Transport: New(nil, rate.Limit(rps), burst, opts...)}
}

// RoundTrip implements [http.RoundTripper]. It blocks until the limiter for the
// request's key permits the call or the request's context is done, then
// delegates to the base RoundTripper. If the context is canceled or its
// deadline is exceeded while waiting, RoundTrip returns that error and does not
// call the base RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	limiter := t.limiterFor(t.keyFunc(req))
	if err := limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// Len reports the number of per-key limiters currently tracked. It is primarily
// useful for observability and tests.
func (t *Transport) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// limiterFor returns the limiter for key, creating it on first use. When
// eviction is enabled it also opportunistically removes idle limiters.
func (t *Transport) limiterFor(key string) *rate.Limiter {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	if t.idle > 0 {
		t.sweepLocked(now)
	}

	e, ok := t.entries[key]
	if !ok {
		e = &hostEntry{limiter: rate.NewLimiter(t.limit, t.burst)}
		t.entries[key] = e
	}
	e.lastAccess = now
	return e.limiter
}

// sweepLocked removes limiters idle for at least t.idle. It runs at most once
// per idle interval so the amortized per-request cost stays low. The caller
// must hold t.mu.
func (t *Transport) sweepLocked(now time.Time) {
	if now.Sub(t.lastSweep) < t.idle {
		return
	}
	t.lastSweep = now
	for key, e := range t.entries {
		if now.Sub(e.lastAccess) >= t.idle {
			delete(t.entries, key)
		}
	}
}
