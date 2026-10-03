-- M7 Edge functions: WASM (WASI) modules run in a wazero sandbox. The module
-- bytes live in the BlobStore (keyed by function id); this table holds metadata,
-- invocation authorization, resource caps, and the secret map injected as env.
-- Portable SQL: TEXT/INTEGER only.

CREATE TABLE _ps_functions (
  id           TEXT PRIMARY KEY,
  slug         TEXT NOT NULL,
  runtime      TEXT NOT NULL,              -- "wasi" (wasip1 command module)
  timeout_ms   INTEGER NOT NULL,
  memory_mb    INTEGER NOT NULL,
  invoke_roles TEXT NOT NULL,              -- JSON array; [] means admin-only
  secrets      TEXT NOT NULL,              -- JSON object {name: value}, injected as env
  etag         TEXT NOT NULL,              -- sha256 hex of the wasm bytes ("" until code uploaded)
  size         INTEGER NOT NULL DEFAULT 0,
  has_code     INTEGER NOT NULL DEFAULT 0, -- 1 once a module is uploaded
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE UNIQUE INDEX ux_ps_functions_slug ON _ps_functions (slug);
