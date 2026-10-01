-- System catalog: metadata about user-defined tables, their columns, and the
-- access policies that guard them. All tables are in the reserved _oc_ namespace.

CREATE TABLE _oc_tables (
  name       TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
);

CREATE TABLE _oc_columns (
  table_name TEXT    NOT NULL REFERENCES _oc_tables(name) ON DELETE CASCADE,
  name       TEXT    NOT NULL,
  type       TEXT    NOT NULL,            -- logical type (text, integer, ...)
  nullable   INTEGER NOT NULL DEFAULT 1,
  is_unique  INTEGER NOT NULL DEFAULT 0,
  ordinal    INTEGER NOT NULL,
  PRIMARY KEY (table_name, name)
);

CREATE TABLE _oc_policies (
  id         TEXT PRIMARY KEY,
  table_name TEXT NOT NULL REFERENCES _oc_tables(name) ON DELETE CASCADE,
  action     TEXT NOT NULL,               -- select | insert | update | delete
  roles      TEXT NOT NULL,               -- JSON array of role names
  using_expr TEXT,                        -- which rows (select/update/delete); null = none
  check_expr TEXT,                        -- allowed new values (insert/update WITH CHECK)
  created_at TEXT NOT NULL
);

CREATE INDEX idx_oc_columns_table  ON _oc_columns(table_name);
CREATE INDEX idx_oc_policies_table ON _oc_policies(table_name, action);
