-- Agent memory: a persistent per-subject, namespaced key/value store.

CREATE TABLE _oc_memory (
  subject    TEXT NOT NULL,
  namespace  TEXT NOT NULL,
  key        TEXT NOT NULL,
  value      TEXT NOT NULL,          -- JSON value
  updated_at TEXT NOT NULL,
  PRIMARY KEY (subject, namespace, key)
);
