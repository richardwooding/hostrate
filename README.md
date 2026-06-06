# hostrate

[![Go Reference](https://pkg.go.dev/badge/github.com/richardwooding/hostrate.svg)](https://pkg.go.dev/github.com/richardwooding/hostrate)
[![Go](https://github.com/richardwooding/hostrate/actions/workflows/go.yml/badge.svg)](https://github.com/richardwooding/hostrate/actions/workflows/go.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/richardwooding/hostrate)](https://goreportcard.com/report/github.com/richardwooding/hostrate)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A tiny, dependency-light `http.RoundTripper` that rate-limits outbound HTTP
requests **per host** (or by any key you choose), so your client stays polite to
each remote server independently instead of sharing one global budget across all
of them.

```go
client := hostrate.NewClient(2, 5) // 2 req/s per host, burst 5
resp, err := client.Get("https://example.com/feed.xml")
```

## Why

Most Go rate-limiting libraries are **inbound** (limit requests *to* your
server, e.g. `go-chi/httprate`, `sethvargo/go-limiter`). For **outbound** calls,
people reach for `golang.org/x/time/rate` and almost always hand-roll the same
`map[host]*rate.Limiter` + mutex pattern — it's even in the
[Go wiki](https://go.dev/wiki/RateLimiting). `hostrate` is that pattern, done
once, properly:

- **Per-host buckets** — being slow to `api.a.com` shouldn't throttle
  `api.b.com`. Each key gets its own token bucket, created lazily.
- **Composable** — it's a plain `http.RoundTripper`. Wrap any base transport
  (your tuned `http.Transport`, an auth transport, a tracing transport) and drop
  it into any `http.Client`. Stack it under retry/circuit-breaker middleware
  such as [failsafe-go](https://github.com/failsafe-go/failsafe-go).
- **Context-aware** — waiting honors the request's context, so deadlines and
  cancellation work while throttled.
- **Bounded memory** — optional idle eviction reclaims limiters for hosts you
  stop talking to, so it's safe even for crawlers that reach unbounded hosts.
- **Small** — one file, standard library plus `golang.org/x/time/rate`.

## Install

```sh
go get github.com/richardwooding/hostrate
```

Requires Go 1.26+.

## Usage

### Convenience client

```go
// 2 requests/second per host, bursting up to 5, over http.DefaultTransport.
client := hostrate.NewClient(2, 5)
```

### Compose over your own transport

```go
import (
    "net/http"
    "time"

    "github.com/richardwooding/hostrate"
    "golang.org/x/time/rate"
)

base := &http.Transport{MaxConnsPerHost: 10 /* your pooling, TLS, proxy, … */}

transport := hostrate.New(base, rate.Limit(2), 5,
    hostrate.WithIdleTimeout(10*time.Minute), // bound memory across many hosts
)
client := &http.Client{Transport: transport}
```

`limit` is a [`rate.Limit`](https://pkg.go.dev/golang.org/x/time/rate#Limit):

- `rate.Limit(2)` — 2 requests per second
- `rate.Every(500 * time.Millisecond)` — one request every 500ms
- `rate.Inf` — no limiting

### Choosing the key

By default requests are keyed by host (`KeyByHost`). Swap in a different key with
`WithKeyFunc`:

```go
// Limit per host+port, so :8443 and :9443 get separate budgets.
hostrate.New(base, rate.Limit(2), 5, hostrate.WithKeyFunc(hostrate.KeyByHostPort))

// Or any custom key — e.g. per API token.
hostrate.WithKeyFunc(func(r *http.Request) string {
    return r.Header.Get("X-Api-Key")
})
```

### Memory and eviction

A limiter is created on first use for each key and, by default, kept for the
Transport's lifetime — ideal when the set of hosts is small and known. If your
key space is effectively unbounded (a crawler, a webhook fan-out), set
`WithIdleTimeout` so limiters unused for that long are evicted:

```go
hostrate.New(base, rate.Limit(2), 5, hostrate.WithIdleTimeout(10*time.Minute))
```

Eviction is performed opportunistically (no background goroutine, nothing to
`Close`). `Transport.Len()` reports how many limiters are currently tracked.

## What this is not

- **Not an inbound limiter.** For limiting requests *to* your server, use
  `go-chi/httprate` or `sethvargo/go-limiter`.
- **Not a retry / circuit-breaker.** Those compose cleanly *above* this — see
  [failsafe-go](https://github.com/failsafe-go/failsafe-go),
  [sony/gobreaker](https://github.com/sony/gobreaker),
  [hashicorp/go-retryablehttp](https://github.com/hashicorp/go-retryablehttp).

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.

## License

[MIT](LICENSE)
