-- Auth & identity system tables.

CREATE TABLE _ps_users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  roles         TEXT NOT NULL,          -- JSON array; defaults to ["authenticated"]
  created_at    TEXT NOT NULL
);

CREATE TABLE _ps_api_keys (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  key_hash   TEXT NOT NULL UNIQUE,      -- SHA-256 of the presented key
  roles      TEXT NOT NULL,             -- JSON array of roles
  created_at TEXT NOT NULL
);

CREATE TABLE _ps_refresh_tokens (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES _ps_users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,      -- SHA-256 of the opaque refresh token
  expires_at TEXT NOT NULL,
  used       INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE TABLE _ps_keypairs (
  kid         TEXT PRIMARY KEY,
  private_pem TEXT NOT NULL,
  public_pem  TEXT NOT NULL,
  active      INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL
);

CREATE INDEX idx_ps_refresh_user ON _ps_refresh_tokens(user_id);
