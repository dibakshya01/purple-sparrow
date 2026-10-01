# Policies (authorization)

Access is **deny-by-default**. A request is allowed only if a policy grants the
caller's role that action on that table. `project_admin` bypasses policies.

## Model
```
{ "table": "...", "action": "select|insert|update|delete",
  "roles": ["anon"|"authenticated"|"project_admin"],
  "using": "<expression>",   // which rows (select/update/delete)
  "check": "<expression>" }  // allowed new values (insert/update)
```
- **select / delete** use `using` (a row filter).
- **insert** uses `check` (validates the new row).
- **update** uses BOTH: `using` selects the row, `check` validates the result. If
  `check` is omitted it defaults to `using` — so a user allowed to edit their own
  rows cannot reassign one to someone else.

## Expression language (safe subset)
Operands: column names, `auth.uid()`, `auth.role()`, string/number/boolean/null
literals. Operators: `= <> < <= > >=`, `and`, `or`, `not`, `in (...)`.
No raw SQL, no functions, no subqueries. Expressions compile to **parameterized**
SQL predicates; values are never interpolated.

## NULL / anon
`auth.uid()` is SQL NULL for anonymous callers, so `auth.uid() = owner_id` matches
no rows for anon even if a row has an empty `owner_id`.

## Examples
- Owner-only read: `using: "auth.uid() = owner_id"`
- Public read: `using: "true"`
- Tenant scope: `using: "tenant_id = 'acme' and auth.role() = 'authenticated'"`
