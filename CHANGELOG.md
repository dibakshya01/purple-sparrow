# Changelog

All notable changes to Purple Sparrow. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project uses SemVer.

## [0.1.0] - 2026-10-04

First tagged release. The full feature surface (M0–M10) is built, tested
end-to-end, and hardened.

### Added
- **M0 — Foundations.** Single static Go binary, typed `PS_` config (secure
  loopback default), structured logging, the agent-native error envelope, chi
  router with baseline middleware, `/healthz` `/readyz` `/v1`, graceful shutdown, CI.
- **M1 — Data + policy spine.** User tables and records CRUD over SQLite; an
  app-layer **policy engine** (deny-by-default, `USING` + `WITH CHECK`, compiled to
  parameterized SQL, fuzz-tested); `/meta` introspection; PostgREST-style filters.
- **M2 — Auth.** Email/password (bcrypt), RS256 access tokens + JWKS, single-use
  refresh rotation, hashed API keys, a real principal resolver.
- **M3 — Agent layer.** Docs-over-API (`/docs`), per-subject agent memory
  (`/v1/memory`), and an advisor (`/advisor`).
- **M4 — MCP + CLI.** An MCP stdio server exposing the backend as agent tools;
  `serve` / `mcp` / `mcp-config` / `version` subcommands.
- **M5 — Postgres engine.** A Postgres `DataEngine` adapter selected by
  `PS_DATABASE_URL`, with cross-engine parity (NULLS-LAST ordering, dialect-aware
  types and LIKE/locking, SQLSTATE constraint mapping) proven by a conformance
  suite against real Postgres in CI.
- **M6 — Storage.** Object storage with a `BlobStore` port: a local-filesystem
  adapter (solo tier) and an S3-compatible adapter (scale tiers, pure-Go SigV4, no
  AWS SDK). Buckets + objects with deny-by-default, ownership-scoped access;
  public buckets; HMAC presigned URLs; per-object size caps.
- **M7 — Edge functions.** Deploy WebAssembly (WASI) modules run in an in-process
  `wazero` sandbox — no filesystem, network, subprocess, or host env; stdin/stdout
  request/response; explicit secret injection; per-call timeout and memory cap;
  deny-by-default invocation by role. Pure-Go, no CGO.
- **M8 — Realtime.** An `EventBus` port with an in-process hub; record mutations
  publish change events delivered over SSE (`GET /v1/realtime`), **filtered per
  subscriber by the policy engine** so a client only receives row changes it is
  authorized to read.
- **M9 — Dashboard.** A dependency-free admin console embedded in the binary
  (served at `/`, no Node/build step): sign in with an admin key, browse tables
  (columns, policies, records), storage buckets/objects, functions, the advisor,
  a live realtime tail, and MCP onboarding. Talks only to the public API.
- **M10 — Hardening & release.** Per-client-IP rate limiting (token bucket,
  `PS_RATE_LIMIT_RPS`); Content-Security-Policy, Permissions-Policy, and
  conditional HSTS; a non-root distroless Docker image; a three-tier deploy guide
  (`DEPLOY.md`) with a Kubernetes manifest; a `scripts/smoke.sh` end-to-end check;
  goreleaser multi-platform build config.
- Launch website (GitHub Pages) and README deck.

### Security
- Deny-by-default access; `project_admin` bypass at a single chokepoint.
- Secrets, API keys, and refresh tokens stored only as hashes; JWT verification
  pinned to RS256 (blocks `alg=none` / RS256→HS256 confusion).
- Non-solo tiers refuse to boot without an explicit `PS_ADMIN_API_KEY`; DB file
  `0600`, data dir `0700`.
- Update read-modify-write is transactional with row locking (closes the
  `WITH CHECK` TOCTOU).
- Storage is ownership-scoped; functions run in a WASI sandbox with no fs/net/
  subprocess and only explicit secrets; realtime events are policy-filtered per
  subscriber. Per-IP rate limiting and CSP/security headers on by default.

### Known limitations
- The policy engine has not had an independent third-party security audit — do
  that before storing real or regulated data.
- Realtime SSE is per-node (in-process bus); multi-replica cross-node fan-out
  needs the planned shared-bus (NATS/Redis) adapter.
- The WASM memory cap is process-wide, not per-function.
- The S3 adapter and Postgres engine are verified in CI, not yet exercised under
  sustained production load.
