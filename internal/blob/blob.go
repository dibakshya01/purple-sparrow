// Package blob defines the BlobStore port and its adapters (ADR: ports-and-
// adapters). Object *bytes* live here; object *metadata and authorization* live
// in the storage service over the data engine. The port is deliberately tiny so
// the local-filesystem adapter (solo tier) and the S3-compatible adapter (scale
// tiers) are interchangeable behind one interface.
//
// Blobs are addressed by an opaque storage key (the object's UUID), never by the
// caller-facing object key — so a hostile object key can never escape the storage
// root via path traversal, and renaming an object never moves bytes.
package blob

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned by Get/Delete when the storage key does not exist.
var ErrNotFound = errors.New("blob not found")

// Store is the object-bytes port.
type Store interface {
	// Put writes the full contents of r under key, returning the number of bytes
	// written and the sha256 hex of those bytes (the etag). Put overwrites.
	Put(ctx context.Context, key string, r io.Reader) (size int64, etag string, err error)
	// Get opens the object for reading. The caller must Close the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object. Deleting a missing key returns ErrNotFound.
	Delete(ctx context.Context, key string) error
	// Kind identifies the adapter ("local", "s3") for diagnostics.
	Kind() string
}
