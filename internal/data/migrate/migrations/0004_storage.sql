-- M6 Storage: buckets and object metadata. Object bytes live in the BlobStore
-- (local filesystem or S3); this table is the policy-enforced index over them.
-- Portable SQL: TEXT/INTEGER only, booleans as INTEGER (0/1), timestamps as ISO
-- TEXT — identical shape on SQLite and Postgres.

CREATE TABLE _ps_buckets (
  name       TEXT PRIMARY KEY,
  public     INTEGER NOT NULL DEFAULT 0,  -- 1 = objects are world-readable
  created_at TEXT NOT NULL
);

CREATE TABLE _ps_objects (
  id           TEXT PRIMARY KEY,          -- UUID; also the blob storage key
  bucket       TEXT NOT NULL,
  object_key   TEXT NOT NULL,             -- caller-facing key (may contain slashes)
  size         INTEGER NOT NULL,
  content_type TEXT NOT NULL,
  etag         TEXT NOT NULL,             -- sha256 hex of the bytes
  owner_id     TEXT,                      -- principal subject; NULL for admin-written
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE UNIQUE INDEX ux_ps_objects_bucket_key ON _ps_objects (bucket, object_key);
CREATE INDEX ix_ps_objects_owner ON _ps_objects (owner_id);

-- Generic server-managed key/value for secrets that must persist across restarts
-- but are generated at runtime (e.g. the storage presign-signing secret). Never
-- exposed over the API.
CREATE TABLE _ps_config (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
