# Contributing to Purple Sparrow

Thanks for your interest. Purple Sparrow is built **spec-driven**: behavior is
specified and reviewed before it's implemented.

## Ground rules

1. **A change starts from a spec.** Non-trivial features begin with a spec and,
   where they change the API, an OpenAPI update. Cross-cutting technical decisions
   get an ADR.
2. **The error envelope is a contract.** Every non-2xx response goes through
   `internal/apierr`. Never return a bare error to a client. Include a useful
   `remediation` and `next_actions` — an agent reads them to self-correct.
3. **Deny by default.** Anything touching data goes through the policy engine.
4. **Tests before merge.** Acceptance criteria become tests. Data-touching code
   must pass on both engines (SQLite and Postgres) once the Postgres adapter lands.

## Development workflow

```bash
make check   # fmt, vet, staticcheck, govulncheck, and race tests — run before pushing
```

- Go 1.27+. Keep the default build `CGO_ENABLED=0` (single static binary).
- Format with `gofmt`; keep dependencies minimal and justified.
- Small, focused PRs. One logical change each.

## Originality / clean-room policy

Purple Sparrow is an independent, clean-room implementation. **Do not copy code,
schema DDL, error strings, or documentation prose from any other project.** You may
reference open standards (OAuth, S3, MCP, SQL) and public API shapes. If you studied
another implementation to understand an idea, close it and write your own. Every PR
affirms this via the checklist in the pull-request template.

## Pull-request checklist

The PR template includes the required checklist (spec/ADR, tests, envelope,
originality, docs). PRs that don't satisfy it will be asked to before review.

## Code of conduct

Be respectful and constructive. Harassment or abuse is not tolerated.
