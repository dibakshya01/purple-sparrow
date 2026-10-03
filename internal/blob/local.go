package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Local is a filesystem-backed BlobStore for the solo tier. Bytes are stored one
// file per object under root, named by the opaque storage key. Writes are atomic
// (temp file + rename) so a crash mid-write never leaves a torn object.
type Local struct{ root string }

// NewLocal creates the storage root (0700) and returns a Local store. It sweeps
// any leftover temp files from a prior crash between write and rename (they are
// never servable — they fail safeKey — but shouldn't accumulate).
func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".tmp-") {
				_ = os.Remove(filepath.Join(root, e.Name()))
			}
		}
	}
	return &Local{root: root}, nil
}

func (l *Local) Kind() string { return "local" }

// safeKey rejects any key that could escape root. Legitimate keys are object
// UUIDs (hex + dashes); anything with a separator, dot segment, or control char
// is refused rather than sanitized.
func safeKey(key string) (string, error) {
	if key == "" || len(key) > 128 {
		return "", fmt.Errorf("invalid blob key length")
	}
	if strings.ContainsAny(key, "/\\") || strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid blob key %q", key)
	}
	for _, r := range key {
		if !(r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return "", fmt.Errorf("invalid blob key %q", key)
		}
	}
	return key, nil
}

func (l *Local) path(key string) (string, error) {
	k, err := safeKey(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(l.root, k), nil
}

func (l *Local) Put(ctx context.Context, key string, r io.Reader) (int64, string, error) {
	dst, err := l.path(key)
	if err != nil {
		return 0, "", err
	}
	tmp, err := os.CreateTemp(l.root, ".tmp-*")
	if err != nil {
		return 0, "", err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we don't reach the successful rename.
	committed := false
	defer func() {
		tmp.Close()
		if !committed {
			os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return 0, "", err
	}
	if err := tmp.Sync(); err != nil {
		return 0, "", err
	}
	if err := tmp.Close(); err != nil {
		return 0, "", err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return 0, "", err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return 0, "", err
	}
	committed = true
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func (l *Local) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

func (l *Local) Delete(ctx context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
