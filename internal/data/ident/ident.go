// Package ident validates and maps user-supplied identifiers and column types.
// Validation happens BEFORE any SQL is built; it is the first line of defense
// against identifier injection (dialect quoting is the second).
package ident

import (
	"fmt"
	"regexp"
	"strings"
)

// pattern: lowercase, starts with a letter or underscore, then letters/digits/
// underscores, max 63 chars total.
var pattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// systemPrefix is reserved for Purple Sparrow's own tables/columns.
const systemPrefix = "_ps_"

// Valid reports whether name is a syntactically valid identifier.
func Valid(name string) bool { return pattern.MatchString(name) }

// ValidUser reports whether name is a valid identifier a user may create: valid
// syntax and not in the reserved _ps_ namespace.
func ValidUser(name string) bool {
	return Valid(name) && !strings.HasPrefix(name, systemPrefix)
}

// columnType maps a whitelisted logical column type to its SQLite storage type.
// The logical set is engine-agnostic; the Postgres adapter (M5) maps the same
// logical types to Postgres types.
var sqliteTypes = map[string]string{
	"text":      "TEXT",
	"integer":   "INTEGER",
	"real":      "REAL",
	"boolean":   "INTEGER", // 0/1
	"timestamp": "TEXT",    // ISO-8601 / RFC3339
	"uuid":      "TEXT",
	"json":      "TEXT",
}

// LogicalTypes returns the set of accepted logical column types.
func LogicalTypes() []string {
	return []string{"text", "integer", "real", "boolean", "timestamp", "uuid", "json"}
}

// SQLiteType returns the SQLite storage type for a logical type, or ok=false if
// the logical type is not whitelisted.
func SQLiteType(logical string) (string, bool) {
	t, ok := sqliteTypes[strings.ToLower(logical)]
	return t, ok
}

// ValidType reports whether logical is a whitelisted column type.
func ValidType(logical string) bool {
	_, ok := sqliteTypes[strings.ToLower(logical)]
	return ok
}

// RequireUser validates a user identifier and returns a descriptive error if it
// is invalid (for surfacing in the API error envelope).
func RequireUser(kind, name string) error {
	if strings.HasPrefix(name, systemPrefix) {
		return fmt.Errorf("%s %q uses the reserved %q prefix", kind, name, systemPrefix)
	}
	if !Valid(name) {
		return fmt.Errorf("%s %q is invalid: use lowercase letters, digits, and underscores; must start with a letter or underscore; max 63 chars", kind, name)
	}
	return nil
}
