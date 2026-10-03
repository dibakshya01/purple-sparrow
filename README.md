<p align="center">
  <img src="docs/assets/social-card.jpg" alt="Purple Sparrow — the agent-native backend" width="820" />
</p>

<h1 align="center">Purple Sparrow</h1>

<p align="center">
  <strong>The agent-native backend.</strong><br>
  A backend-as-a-service built for AI coding agents — deny-by-default policies,
  self-correcting errors, one-call introspection, and native MCP — in a single Go binary.
</p>

<p align="center">
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-Apache--2.0-6d4aff"></a>
  <img alt="go" src="https://img.shields.io/badge/go-1.27-00ADD8">
  <img alt="binary" src="https://img.shields.io/badge/ships%20as-single%20static%20binary-6d4aff">
  <img alt="engines" src="https://img.shields.io/badge/SQLite-first%20·%20Postgres%20for%20scale-16a34a">
  <img alt="mcp" src="https://img.shields.io/badge/MCP-native-8e79ff">
  <img alt="status" src="https://img.shields.io/badge/status-alpha-f59e0b">
  <img alt="PRs" src="https://img.shields.io/badge/PRs-welcome-brightgreen">
</p>

<p align="center">
  <a href="https://dibakshya01.github.io/purple-sparrow/">Website</a> ·
  <a href="https://dibakshya01.github.io/purple-sparrow/eli5.html">In plain English</a> ·
  <a href="https://dibakshya01.github.io/purple-sparrow/docs.html">Docs</a> ·
  <a href="https://dibakshya01.github.io/purple-sparrow/architecture.html">Architecture</a>
</p>

---

## Why

Generic backends were built for humans clicking dashboards. **Coding agents** operate
through APIs — and generic backends fail them in three ways: they fail *silently*,
they leak data through *missing* access rules, and they're *undiscoverable*. Purple
Sparrow is designed so an agent can drive it correctly and recover on its own.

| | |
|---|---|
| 🛡️ **Secure by default** | Access is deny-by-default. An app-layer policy engine compiles rules into **parameterized** SQL with `USING` *and* `WITH CHECK`. Fuzz-tested; the compiler never emits a raw literal. |
| 🧭 **Self-correcting** | Every error carries machine-readable `remediation` + `next_actions`. When an agent hits a wall, the response tells it how to get past it. |
| 🔎 **Discoverable** | `GET /meta` returns the whole backend's shape in one call. Docs are served over the API. An advisor lints the config. |
| 🪶 **Runs anywhere** | One static binary + embedded SQLite — zero dependencies for a solo dev. Point `PS_DATABASE_URL` at Postgres to scale. Same code, same behavior (CI proves parity). |
| 🔌 **MCP-native** | `purplesparrow mcp` exposes the whole backend as tools for Cursor, Claude, and friends. |

## Quick start

```bash
# one static binary, embedded SQLite — no external services
CGO_ENABLED=0 go build -o purplesparrow ./cmd/purplesparrow
PS_ADMIN_API_KEY=ps_sk_dev ./purplesparrow            # → 127.0.0.1:8787

ADMIN="Authorization: Bearer ps_sk_dev"

# create a table (id + created_at are automatic)
curl -s -H "$ADMIN" -X POST localhost:8787/v1/tables \
  -d '{"name":"todos","columns":[{"name":"title","type":"text"},{"name":"owner_id","type":"text"}]}'

# access is deny-by-default — grant a policy, then use records
curl -s -H "$ADMIN" -X POST localhost:8787/v1/policies \
  -d '{"table":"todos","action":"select","roles":["anon"],"using":"true"}'
curl -s -H "$ADMIN" -X POST localhost:8787/v1/tables/todos/records -d '{"title":"ship","owner_id":"u1"}'
curl -s localhost:8787/v1/tables/todos/records

# introspect · read docs · lint config
curl -s -H "$ADMIN" localhost:8787/meta
curl -s localhost:8787/docs
curl -s -H "$ADMIN" localhost:8787/advisor
```

Connect an agent: `purplesparrow mcp-config` prints a client config snippet;
`purplesparrow mcp` runs the MCP stdio server.

## What's inside

```
Agents (MCP · CLI · REST)
        │
   HTTP gateway  — request-id · authn · recovery · access-log · security-headers
        │
  Data+Records · Auth · Storage · Functions · Realtime · Agent layer · Dashboard
        │
  ┌───────────────── Policy engine (deny-by-default, USING + WITH CHECK) ─────────────────┐
  │                   the authorization boundary — one place to get right                  │
  └───────────────────────────────────────────────────────────────────────────────────────┘
        │
   DataEngine · BlobStore · FuncRunner · EventBus ports
        │
   SQLite + local FS (solo)      |      Postgres + S3 (scale)
```

- **Data + policy** — tables, records CRUD, PostgREST-style filters; every read row-filtered, every write checked.
- **Auth** — email/password, RS256 JWTs via JWKS, single-use refresh rotation, hashed API keys.
- **Storage** — buckets + objects, ownership-scoped access, public buckets, presigned URLs; local FS or S3.
- **Functions** — edge functions as sandboxed WASM (WASI/wazero): no fs/net/subprocess, explicit secrets, timeout + memory caps.
- **Realtime** — row-change events over SSE, filtered per subscriber by the policy engine.
- **Agent layer** — docs-over-API, per-subject memory, an advisor for risky config.
- **Dashboard** — a dependency-free admin console embedded in the binary, served at `/`.
- **MCP + CLI** — the whole surface as agent tools; `serve` / `mcp` / `mcp-config` / `version`.

## Status — honest about limits

**Beta.** The full feature surface is built and tested end-to-end — review it before trusting it with regulated data.

- ✅ Built: M0 foundations · M1 data + policy spine · M2 auth · M3 agent layer · M4 MCP/CLI · M5 Postgres · M6 storage · M7 functions · M8 realtime · M9 dashboard · M10 hardening.
- 🧪 Quality gates: `go test -race`, `go vet`, `staticcheck`, `govulncheck` — all green. Postgres parity proven in CI.
- 🔬 Reviewed: spec-reviewed per milestone, plus repeated back-to-back adversarial review rounds.
- 🔒 Hardened: deny-by-default everywhere, per-IP rate limiting, CSP + security headers, non-root distroless image, three-tier [deploy guide](DEPLOY.md).
- ⚠️ Honest caveats: the policy engine warrants an **independent security audit** before regulated data; realtime SSE is per-node (a shared-bus adapter for cross-node fan-out is the next step); the WASM memory cap is process-wide, not per-function; S3 and Postgres are CI-verified, not yet battle-tested under production load.

## Development

```bash
make build     # single static binary
make test      # go test -race ./...
make check     # fmt + vet + staticcheck + govulncheck + test (matches CI)
```

## Contributing & security

Spec-driven, deny-by-default, tests required — see [CONTRIBUTING.md](CONTRIBUTING.md).
Report vulnerabilities privately via [SECURITY.md](SECURITY.md), not public issues.

## License

[Apache-2.0](LICENSE). Built in the open. Not affiliated with any other backend platform.
