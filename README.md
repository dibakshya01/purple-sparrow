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

**Pre-alpha, under active construction.** This repository is being built milestone
by milestone (spec-driven). What exists today:

- **M0 — Foundations** ✅ single static binary, typed config, structured logging,
  HTTP router with the agent-native error envelope, health/readiness, graceful
  shutdown, CI.

Next: **M1** the data + policy engine (the spine), then auth, then the agent layer.

## Quickstart (M0)

```bash
# build a single static binary
CGO_ENABLED=0 go build -o purplesparrow ./cmd/purplesparrow

# run it
./purplesparrow
# → listening on 127.0.0.1:8787

curl -s http://127.0.0.1:8787/healthz   # {"status":"ok"}
curl -s http://127.0.0.1:8787/v1        # service metadata
```

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
