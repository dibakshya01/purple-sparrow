# Security Policy

Purple Sparrow is backend infrastructure — security is a first-class concern.

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately via GitHub's **"Report a vulnerability"** (Security → Advisories)
on the repository, or by emailing the maintainer. Include:

- a description and impact,
- steps to reproduce (a proof of concept if possible),
- affected version/commit.

We aim to acknowledge within a few days and to coordinate a fix and disclosure
timeline with you.

## Scope of particular interest

- **The policy / predicate engine** (`internal/policy`) — the authorization
  boundary. Any way to read or write data that a policy should forbid is critical.
- **Auth & tokens** — token forgery, JWKS handling, key/anon-key validation.
- **Function sandbox** — any escape from the WASM sandbox (filesystem, network,
  environment, or subprocess access that should be denied).
- **Secret handling** — leakage of secrets in logs, errors, or responses.

## Supported versions

Pre-alpha: only the latest `main` is supported. A support policy will accompany the
first tagged release.
