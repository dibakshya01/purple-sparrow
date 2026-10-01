# Changelog

All notable changes to Orange Crow. This project is in **alpha**; the format
follows [Keep a Changelog](https://keepachangelog.com/), and versioning will be
SemVer from the first tagged release.

## [Unreleased]

### Added
- **M0 — Foundations.** Single static Go binary, typed `OC_` config (secure
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
  `OC_DATABASE_URL`, with cross-engine parity (NULLS-LAST ordering, dialect-aware
  types and LIKE/locking, SQLSTATE constraint mapping) proven by a conformance
  suite against real Postgres in CI.
- Launch website (GitHub Pages) and README deck.

### Security
- Deny-by-default access; `project_admin` bypass at a single chokepoint.
- Secrets, API keys, and refresh tokens stored only as hashes; JWT verification
  pinned to RS256 (blocks `alg=none` / RS256→HS256 confusion).
- Non-solo tiers refuse to boot without an explicit `OC_ADMIN_API_KEY`; DB file
  `0600`, data dir `0700`.
- Update read-modify-write is transactional with row locking (closes the
  `WITH CHECK` TOCTOU).

### Not yet implemented
- Storage, edge functions, realtime, and the web dashboard (designed, not built).
- The policy engine has not had an independent third-party security audit.
