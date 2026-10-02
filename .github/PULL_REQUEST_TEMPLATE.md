<!-- Thanks for contributing to Purple Sparrow. Keep PRs small and focused. -->

## What & why

<!-- What does this change, and why? Link the spec / ADR / issue. -->

- Spec / ADR:
- Closes:

## Checklist

- [ ] Behavior is covered by an approved spec (or is a trivial fix that doesn't need one).
- [ ] API changes are reflected in `/openapi`.
- [ ] New cross-cutting decision recorded as an ADR.
- [ ] Tests added/updated; `make check` passes locally (fmt, vet, staticcheck, govulncheck, race tests).
- [ ] Data-touching changes pass on **both** engines (SQLite and Postgres) — or Postgres N/A yet.
- [ ] Every non-2xx path returns the error envelope with a real `remediation`.
- [ ] Deny-by-default preserved for anything touching data.
- [ ] **Originality:** no code, schema, error strings, or docs prose copied from another project; only open standards / public API shapes referenced.
- [ ] Docs updated where user-facing behavior changed.

## Notes for reviewers

<!-- Anything you want a reviewer to look at closely. -->
