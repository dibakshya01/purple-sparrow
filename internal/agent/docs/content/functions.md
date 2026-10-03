# Edge functions (WASM)

Deploy custom logic as a **WebAssembly (WASI) module**. Purple Sparrow runs it
in an in-process **wazero** sandbox: no filesystem, no network, no subprocess,
and only the environment you explicitly inject. Each call has a timeout and a
memory cap.

## The contract

A function is a `wasip1` command module. On each invocation:

- the **request body** arrives on the guest's **stdin**;
- the response is whatever the guest writes to **stdout** (exit 0 → `200`);
- a **non-zero exit** becomes `502 function_error` (stderr is logged server-side);
- the invocation **context** and the function's **secrets** are the only
  environment variables the guest sees:
  - `PS_METHOD`, `PS_QUERY`, `PS_PRINCIPAL_SUBJECT`, `PS_PRINCIPAL_ROLES`
  - each secret you configured, by name.

Compile from any language that targets WASI. For Go:

```
GOOS=wasip1 GOARCH=wasm go build -o fn.wasm .
```

## Endpoints

| Method & path | Who | Purpose |
|---|---|---|
| `POST /v1/functions` | admin | Create metadata: `{"slug","invoke_roles":[…],"secrets":{…},"timeout_ms"}` |
| `PUT /v1/functions/{slug}/code` | admin | Upload (or replace) the `.wasm` module (raw body) |
| `GET /v1/functions` | admin | List functions (secret *values* are never returned) |
| `GET /v1/functions/{slug}` | admin | Describe a function |
| `DELETE /v1/functions/{slug}` | admin | Delete a function and its code |
| `POST /v1/fn/{slug}` | per `invoke_roles` | Invoke; request body → stdin, stdout → response |

## Authorization

Invocation is **deny-by-default**. A function runs only for a caller whose role
is listed in `invoke_roles` (admin always may). An empty `invoke_roles` means
**admin-only**. Use `["anon"]` for a public function, `["authenticated"]` for
signed-in users.

## Limits

- `timeout_ms` (default 5000, max 60000) — exceeding it returns `504 function_timeout`.
- Memory is a **process-wide** cap (`PS_FN_MAX_MEMORY_MB`, default 128), shared by
  all functions — not a per-function limit.
- Module size ≤ 32 MiB.

## Example

```
POST /v1/functions
{ "slug": "slugify", "invoke_roles": ["authenticated"],
  "secrets": { "SALT": "…" } }

PUT  /v1/functions/slugify/code      <fn.wasm bytes>   → 200

POST /v1/fn/slugify   body: "Hello World"              → 200 "hello-world"
```
