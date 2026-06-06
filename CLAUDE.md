# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`hostrate` is a single-package Go library providing an `http.RoundTripper` that
rate-limits **outbound** HTTP requests per host (or by any custom key). Each key
gets its own lazily-created `golang.org/x/time/rate.Limiter`. It is intentionally
tiny: the entire implementation lives in `hostrate.go`.

## Commands

```sh
go build -v ./...
go test -race -v ./...                       # full suite (CI runs with -race)
go test -run TestPerHostIsolation ./...      # single test
go test -run Example ./...                   # run the runnable examples (they assert Output:)
golangci-lint run                            # CI uses golangci-lint v2 (config in .golangci.yml)
```

CI (`.github/workflows/go.yml`) runs build, `go test -race`, and golangci-lint
on push/PR to `main`. Requires Go 1.26+.

## Architecture

The public surface is small and worth keeping coherent:

- `New(base, limit, burst, ...Option) *Transport` — the core constructor. `base`
  nil falls back to `http.DefaultTransport`. Returns a `*Transport` that wraps any
  base `RoundTripper`, so it composes under retry/circuit-breaker middleware and
  over tuned `http.Transport`s.
- `NewClient(rps, burst, ...Option) *http.Client` — convenience wrapper around `New`.
- `RoundTrip` blocks on `limiter.Wait(req.Context())` before delegating, so
  cancellation/deadlines are honored and a canceled request never reaches `base`.
- `KeyFunc` (with `KeyByHost` default and `KeyByHostPort`) decides which requests
  share a limiter. Configurable via `WithKeyFunc`. `WithIdleTimeout` enables
  eviction.

### Eviction is opportunistic — no background goroutine

There is deliberately nothing to `Close()`. Idle limiters are swept inside
`limiterFor` (called on every request) via `sweepLocked`, which runs at most once
per `idle` interval to keep amortized per-request cost low. When changing eviction
logic, preserve this property: all map access is guarded by `t.mu`, and the sweep
must stay cheap because it sits on the request hot path.

### Testable clock

`Transport.now func() time.Time` exists solely to let tests inject a fake clock.
Tests that exercise eviction timing live in the **white-box** `package hostrate`
test file (`hostrate_test.go`) and set `tr.now` directly. Runnable examples live
in the **black-box** `package hostrate_test` file (`example_test.go`) and assert
`// Output:`. Keep timing-sensitive tests deterministic by driving `tr.now` rather
than sleeping.

## Conventions

- This is a public library (`pkg.go.dev`): all exported identifiers carry doc
  comments using `[bracket]` doc links. Match that style when adding API.
- The linter is strict (gosec, gocyclo/gocognit complexity caps, revive `exported`
  rule, gocritic with all tags). Keep functions under the complexity thresholds
  (gocyclo 15, gocognit 20). Test files are exempted from several linters — see the
  `exclusions` block in `.golangci.yml`.
- Formatting is enforced by gofmt + goimports + gofumpt, with local import prefix
  `github.com/richardwooding/hostrate`.
- Dependencies are minimal by design: stdlib plus `golang.org/x/time/rate`. Avoid
  adding dependencies.
