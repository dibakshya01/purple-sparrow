# Deploying Purple Sparrow

One binary, three tiers. The same build runs solo on a laptop or horizontally
scaled behind Postgres + S3 — you switch tiers with environment variables, not a
different artifact.

| Tier | Data engine | Blobs | Scale | Use |
|---|---|---|---|---|
| **T0 — solo** | embedded SQLite | local filesystem | one process | laptops, demos, small internal tools |
| **T1 — startup** | Postgres | local or S3 | a few replicas | production for small teams |
| **T2 — enterprise** | Postgres (HA) | S3 | many replicas | HA, horizontal scale |

## Before you start (every tier)

- **Set an admin key.** `PS_ADMIN_API_KEY=ps_sk_<random>`. The solo tier will
  generate and log an ephemeral one if you don't; **non-solo tiers refuse to boot
  without it** (a per-restart secret in production logs is both a leak and
  useless). Generate one: `echo "ps_sk_$(openssl rand -hex 16)"`.
- **Bind address.** Defaults to loopback `127.0.0.1:8787` (secure by default). To
  expose it, set `PS_ADDR=0.0.0.0:8787` (the Docker image does this).
- **Put TLS in front.** Terminate HTTPS at a load balancer / reverse proxy;
  Purple Sparrow sets HSTS when it sees `X-Forwarded-Proto: https`.
- **Rate limiting** is on by default (100 rps/IP, burst 200). Tune with
  `PS_RATE_LIMIT_RPS` / `PS_RATE_LIMIT_BURST` (0 disables).

## T0 — Solo (single binary)

```bash
go install github.com/dibakshya01/purple-sparrow/cmd/purplesparrow@latest
PS_ADMIN_API_KEY=ps_sk_$(openssl rand -hex 16) purplesparrow serve
```

State (SQLite DB, signing key, local blobs) lives in `./.purplesparrow`
(`PS_DATA_DIR`). The dir is created `0700`, the DB `0600`. Back up that directory.

## T1 — Startup (Docker + Postgres)

```bash
docker run -p 8787:8787 -v ps-data:/data \
  -e PS_ADMIN_API_KEY=ps_sk_... \
  -e PS_DATABASE_URL=postgres://user:pass@host:5432/db \
  ghcr.io/dibakshya01/purple-sparrow:latest
```

Setting `PS_DATABASE_URL` selects the Postgres engine (and implies a non-solo
tier). Migrations run automatically at boot under an advisory lock, so rolling
multiple replicas is safe. The volume at `/data` still holds the signing key and
(if `PS_STORAGE_BACKEND=local`) blobs — for more than one replica, use S3 (below)
so pods stay stateless.

## T2 — Enterprise (Kubernetes + Postgres + S3)

Use S3 for blobs so every pod is stateless:

```
PS_STORAGE_BACKEND=s3
PS_S3_ENDPOINT=https://s3.us-east-1.amazonaws.com
PS_S3_REGION=us-east-1
PS_S3_BUCKET=my-ps-objects
PS_S3_ACCESS_KEY_ID=...        # prefer a secret store
PS_S3_SECRET_ACCESS_KEY=...
```

A ready-to-edit manifest (Deployment with 2 replicas, readiness/liveness probes,
non-root, read-only rootfs, dropped capabilities) is in
[`deploy/kubernetes.yaml`](deploy/kubernetes.yaml). Point `PS_DATABASE_URL` at
your managed/HA Postgres. Scale with `kubectl scale deployment/purple-sparrow
--replicas=N`.

> Realtime (SSE) is per-node in-process. With multiple replicas, a subscriber
> only receives events produced on the node it's connected to. A shared event bus
> (NATS/Redis) adapter is the planned path to cross-node fan-out; until then, a
> single realtime replica or sticky sessions keeps delivery complete.

## Smoke test

After deploying, verify the core path end-to-end:

```bash
PS_ADMIN_API_KEY=ps_sk_... PS_BASE=https://your-host ./scripts/smoke.sh
```

## Operations

- **Health:** `GET /healthz` (liveness), `GET /readyz` (readiness; flips to 503
  during graceful shutdown).
- **Logs:** structured JSON (`PS_LOG_FORMAT=json`); every request/error carries a
  `request_id` for correlation.
- **Backups:** T0 — back up `PS_DATA_DIR`. T1/T2 — back up Postgres and the S3
  bucket; keep `PS_ADMIN_API_KEY` and the row `storage_sign_secret` in `_ps_config`.
