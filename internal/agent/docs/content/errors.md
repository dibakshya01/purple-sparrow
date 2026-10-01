# Errors

Every non-2xx response uses one envelope, designed for an agent to self-correct:
```
{ "error": {
  "code": "policy_denied",
  "message": "No policy grants this action to your role.",
  "remediation": "Add a policy (POST /v1/policies) ... or authenticate as project_admin.",
  "next_actions": ["GET /meta", "POST /v1/policies"],
  "doc_ref": "/docs/policies",
  "request_id": "..." } }
```
Read `remediation` and `next_actions` and act on them. `request_id` also appears
in the `X-Request-Id` header and server logs.

## Common codes
- `not_found` / `method_not_allowed` — routing.
- `invalid_credentials` (401) — bad token/key. `admin_required` (403) — needs admin.
- `table_not_found` / `table_exists` — table lifecycle.
- `invalid_identifier` / `invalid_column_type` — bad DDL input.
- `policy_denied` (403) — deny-by-default; add a policy or elevate.
- `policy_invalid_expr` / `policy_invalid` — bad policy definition.
- `invalid_filter` — bad query filter (`?col=op.value`, op in eq,ne,lt,lte,gt,gte,like,in).
- `validation_failed` — unknown/read-only column, or missing values.
- `record_not_found` — no row matched (may not exist, or a policy hides it).
- `internal` (500) — server error; retry; cite `request_id` if it persists.
