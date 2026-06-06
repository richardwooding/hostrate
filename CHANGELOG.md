# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-06-07

Initial release.

### Added

- `Transport`, an `http.RoundTripper` that applies a per-key token-bucket rate
  limit before delegating to a base transport. Limiters are created lazily per
  key and the type is safe for concurrent use.
- `New(base, limit, burst, ...Option)` constructor; `base` defaults to
  `http.DefaultTransport` when nil.
- `NewClient(rps, burst, ...Option)` convenience constructor returning a
  rate-limited `*http.Client`.
- Key functions `KeyByHost` (default) and `KeyByHostPort`, configurable via the
  `WithKeyFunc` option.
- `WithIdleTimeout` option for opportunistic eviction of idle limiters, bounding
  memory when the key space is effectively unbounded.
- `Transport.Len()` for observability into the number of tracked limiters.
- Context-aware waiting: a canceled or expired request returns its context error
  and never reaches the base transport.

[Unreleased]: https://github.com/richardwooding/hostrate/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/richardwooding/hostrate/releases/tag/v0.1.0
