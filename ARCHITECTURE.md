# Architecture

Purple Sparrow is **ports-and-adapters** throughout: every external capability sits
behind an interface, and adapters are chosen at startup. One build serves a solo
laptop and a scaled cloud deployment.

```
Agents (MCP · CLI · REST)
        │
   HTTP gateway  — request-id · authn · recovery · access-log · security-headers
        │
  Data+Records · Auth · Agent layer (docs/memory/advisor)   [Storage/Functions/Realtime — planned]
        │
  ┌──────────── Policy engine — the authorization boundary ────────────┐
  │  deny-by-default · rules → parameterized SQL · USING + WITH CHECK   │
  └─────────────────────────────────────────────────────────────────────┘
        │
   DataEngine port  →  SQLite adapter (solo)   |   Postgres adapter (scale)
```

## Layout

```
cmd/purplesparrow/      main: config, engine selection, wiring, graceful shutdown
internal/
  apierr/               agent error envelope (code, message, remediation, next_actions)
  reqid/                request-id context + middleware
  config/               typed PS_ config, tier resolution
  httpapi/              chi router, middleware, handlers, auth bridge, error mapping
  data/                 Engine port + Dialect; sqlite.go, postgres.go; ident/, migrate/
  catalog/              user-table DDL + _ps_ metadata (transactional)
  policy/               lexer → parser → compiler (parameterized predicates) + Enforcer
  records/              policy-enforced CRUD + type normalization + filters
  auth/                 bcrypt, RS256 JWT + JWKS, refresh rotation, API keys, resolver
  agent/{meta,docs,memory,advisor}   the agent-native layer
  mcp/                  MCP stdio server (JSON-RPC) exposing the API as tools
  idgen/, principal/, buildinfo/, observability/, web/
docs/                   the GitHub Pages website (this is the site, not the API docs)
openapi/                OpenAPI 3.1 source of truth
```

## Load-bearing decisions

- **The policy engine is the authorization boundary** (`internal/policy`). A single
  `Enforcer` compiles policy expressions into **parameterized** SQL predicates —
  identical on SQLite and Postgres. Deny-by-default; `project_admin` bypasses at one
  chokepoint. `auth.uid()` is SQL NULL for anon. Fuzz-tested.
- **One logical model, two engines.** The `DataEngine` port + a `Dialect`
  (quoting, placeholder style, type map, LIKE/lock clause) keep behavior identical.
  A conformance suite runs the security + correctness behaviors against real Postgres
  in CI. Writes that read-modify-write (UPDATE `WITH CHECK`) run in one transaction
  with row locking so the check can't be raced.
- **Errors are a contract.** Every non-2xx uses the versioned envelope with a
  `remediation` and `next_actions` — the agent-repair-loop affordance.
- **Secure by default.** Deny-by-default access; secrets/keys hashed; the DB file
  (`0600`) and data dir (`0700`) locked down; non-solo tiers refuse to boot without
  an explicit `PS_ADMIN_API_KEY`.

## Dependency direction

`cmd` wires concrete adapters; `internal/httpapi` depends on services; services depend
on the `data.Engine` port, never on a concrete engine. Nothing under `internal/*`
imports `httpapi`. Adapters depend on ports, not the reverse.

For the deep version (specs, ADRs, milestone plans), see the design docs; the website's
[Architecture page](https://dibakshya01.github.io/purple-sparrow/architecture.html) has
diagrams.
