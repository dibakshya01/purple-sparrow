<h1 align="center">Purple Sparrow</h1>

<p align="center">
  <strong>The agent-native backend platform.</strong><br>
  A backend-as-a-service built for AI coding agents first — shipped as a single static binary.
</p>

<p align="center">
  <a href="#status"><img alt="status" src="https://img.shields.io/badge/status-pre--alpha-a78bfa"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="go" src="https://img.shields.io/badge/go-1.27-00ADD8">
</p>

---

## What is this?

Purple Sparrow is an open-source **backend-as-a-service whose primary operator is a
coding agent** (via MCP and a CLI), and whose secondary operator is a human (via a
dashboard and REST). It gives an agent database, auth, storage, functions, and
realtime — and, unlike a generic BaaS, it actively helps the agent *operate it
correctly*: machine-readable error remediation, one-call introspection, and
docs-over-API.

It runs the same everywhere:

| You are… | You run… | Dependencies |
|---|---|---|
| a solo dev / vibecoder | one binary | **none** (embedded SQLite) |
| a startup | the binary + Postgres | Postgres |
| an enterprise | container(s) on k8s | Postgres, S3, event bus |

## Why "agent-native"?

Every error tells the agent how to fix it. `GET /meta` returns the whole backend
shape in one call. The docs are served over the API. One authorization model works
identically on SQLite and Postgres. The whole surface is exposed as MCP tools so an
agent can operate the backend like a backend engineer.

## Status

**Alpha, under active construction.** Built milestone by milestone (spec-driven).
What works today:

- **M0 — Foundations** ✅ single static binary, typed config, structured logging,
  agent-native error envelope, health/readiness, graceful shutdown, CI.
- **M1 — Data + policy spine** ✅ user tables, records CRUD, an app-layer policy
  engine (deny-by-default, USING + WITH CHECK, fuzz-tested, parameterized),
  `/meta` introspection.
- **M2 — Auth** ✅ email/password, RS256 access tokens + JWKS, single-use refresh
  rotation, hashed API keys, real principal resolver.
- **M3 — Agent layer** ✅ docs over the API (`/docs`), per-subject agent memory,
  and an advisor (`/advisor`).
- **M4 — MCP + CLI** ✅ operate the whole backend as MCP tools (`purplesparrow mcp`).

Next (not yet built): **M5** a Postgres engine for scale, then storage, edge
functions, realtime, the dashboard, and release hardening. Those adapters are
designed (see the architecture) but not implemented — don't expect them yet.

## Quickstart

```bash
# build a single static binary
CGO_ENABLED=0 go build -o purplesparrow ./cmd/purplesparrow

# run it (solo tier: embedded SQLite, no external deps)
PS_ADMIN_API_KEY=ps_sk_dev ./purplesparrow      # → 127.0.0.1:8787

ADMIN="Authorization: Bearer ps_sk_dev"

# create a table (id + created_at are added automatically)
curl -s -H "$ADMIN" -X POST localhost:8787/v1/tables \
  -d '{"name":"todos","columns":[{"name":"title","type":"text"},{"name":"owner_id","type":"text"}]}'

# access is deny-by-default — grant a role, then use records
curl -s -H "$ADMIN" -X POST localhost:8787/v1/policies \
  -d '{"table":"todos","action":"select","roles":["anon"],"using":"true"}'
curl -s -H "$ADMIN" -X POST localhost:8787/v1/tables/todos/records -d '{"title":"ship","owner_id":"u1"}'
curl -s localhost:8787/v1/tables/todos/records

# introspect, read docs, run the advisor
curl -s -H "$ADMIN" localhost:8787/meta
curl -s localhost:8787/docs
curl -s -H "$ADMIN" localhost:8787/advisor

# user auth
curl -s -X POST localhost:8787/v1/auth/signup -d '{"email":"a@b.com","password":"password1"}'
```

Connect a coding agent over MCP: `purplesparrow mcp-config` prints a client config
snippet; `purplesparrow mcp` runs the MCP stdio server.

> Note: `PS_ADMIN_API_KEY` is required for non-solo tiers (the server refuses to
> boot without it rather than log an ephemeral secret). In solo tier it's
> auto-generated and logged for convenience.

Configuration is via `PS_`-prefixed environment variables:

| Var | Default | Meaning |
|---|---|---|
| `PS_ADDR` | `127.0.0.1:8787` | listen address (use `0.0.0.0:8787` in containers) |
| `PS_LOG_LEVEL` | `info` | `debug`\|`info`\|`warn`\|`error` |
| `PS_LOG_FORMAT` | `json` | `json`\|`text` |
| `PS_DATA_DIR` | `./.purplesparrow` | data directory |
| `PS_TIER` | `auto` | `auto`\|`solo`\|`startup`\|`enterprise` |

## Development

```bash
make build     # build the binary
make test      # go test -race ./...
make check     # fmt + vet + staticcheck + govulncheck + test
make run       # build and run
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE).
