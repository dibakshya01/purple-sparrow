// Package memory is the agent memory store: a persistent, per-subject, namespaced
// key/value store so an agent can keep state across sessions. Entries are
// isolated by the resolved subject — one subject can never read another's.
package memory

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/ident"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
)

// ErrNotFound is returned when a key does not exist for the subject.
var ErrNotFound = errors.New("memory key not found")

// ErrInvalidName is returned for an invalid namespace or key.
var ErrInvalidName = errors.New("invalid namespace or key")

// Entry is a stored memory item.
type Entry struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// Service implements the memory store.
type Service struct{ eng data.Engine }

// New returns a memory Service.
func New(eng data.Engine) *Service { return &Service{eng: eng} }

func valid(name string) bool { return ident.Valid(name) }

// Set upserts a value for (subject, namespace, key).
func (s *Service) Set(ctx context.Context, subject, namespace, key string, value any) error {
	if !valid(namespace) || !valid(key) {
		return ErrInvalidName
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.eng.ExecCtx(ctx,
		`INSERT INTO _ps_memory (subject, namespace, key, value, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(subject, namespace, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		subject, namespace, key, string(raw), idgen.NowRFC3339())
	return err
}

// Get returns a value, or ErrNotFound.
func (s *Service) Get(ctx context.Context, subject, namespace, key string) (*Entry, error) {
	row, err := s.eng.QueryRowCtx(ctx,
		`SELECT value, updated_at FROM _ps_memory WHERE subject = ? AND namespace = ? AND key = ?`,
		subject, namespace, key)
	if errors.Is(err, data.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Entry{Namespace: namespace, Key: key, Value: decode(str(row["value"])), UpdatedAt: str(row["updated_at"])}, nil
}

// List returns all entries in a namespace for the subject.
func (s *Service) List(ctx context.Context, subject, namespace string) ([]Entry, error) {
	rows, err := s.eng.QueryCtx(ctx,
		`SELECT key, value, updated_at FROM _ps_memory WHERE subject = ? AND namespace = ? ORDER BY key`,
		subject, namespace)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, Entry{Namespace: namespace, Key: str(r["key"]), Value: decode(str(r["value"])), UpdatedAt: str(r["updated_at"])})
	}
	return out, nil
}

// Delete removes a key; returns ErrNotFound if absent.
func (s *Service) Delete(ctx context.Context, subject, namespace, key string) error {
	n, err := s.eng.ExecCtx(ctx,
		`DELETE FROM _ps_memory WHERE subject = ? AND namespace = ? AND key = ?`, subject, namespace, key)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func decode(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
