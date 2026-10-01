# Getting started

Purple Sparrow is an agent-native backend. You create tables, insert and query
records, and guard everything with policies — over a REST API designed for a
coding agent to operate.

## 1. Authenticate
Use an admin API key for setup (header `Authorization: Bearer ps_sk_...`), or a
user access token from `/v1/auth/login`.

## 2. Create a table
```
POST /v1/tables
{ "name": "todos", "columns": [
  { "name": "title", "type": "text" },
  { "name": "owner_id", "type": "text" },
  { "name": "done", "type": "boolean" } ] }
```
`id` (uuid) and `created_at` (timestamp) are added automatically. Column types:
text, integer, real, boolean, timestamp, uuid, json.

## 3. Add a policy (access is deny-by-default)
Without a policy, only `project_admin` can touch a table. Grant access:
```
POST /v1/policies
{ "table":"todos", "action":"select", "roles":["authenticated"],
  "using":"auth.uid() = owner_id" }
```

## 4. Use records
- `POST /v1/tables/todos/records` — insert (object or array)
- `GET  /v1/tables/todos/records?done=eq.false&order=created_at.desc&limit=20`
- `GET/PATCH/DELETE /v1/tables/todos/records/{id}`

## 5. Introspect
`GET /meta` returns the whole backend shape (tables, columns, policies) in one
call. `GET /advisor` flags configuration issues. `GET /docs` lists these docs.
