package blob

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLocalRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "0123abcd-0000-1111-2222-333344445555"
	n, etag, err := store.Put(ctx, key, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if n != 5 {
		t.Fatalf("size = %d, want 5", n)
	}
	// sha256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if etag != want {
		t.Fatalf("etag = %s, want %s", etag, want)
	}
	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" {
		t.Fatalf("content = %q", string(b))
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing = %v, want ErrNotFound", err)
	}
}

func TestLocalRejectsUnsafeKeys(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, bad := range []string{"../escape", "a/b", "..", "with space", "sub\\dir", ""} {
		if _, _, err := store.Put(ctx, bad, strings.NewReader("x")); err == nil {
			t.Fatalf("unsafe key %q should be rejected", bad)
		}
	}
}

// TestSigV4SigningKey verifies the SigV4 key-derivation chain against AWS's
// published example (docs: "Examples of how to derive a signing key"), which
// pins the exact HMAC chain the S3 adapter relies on.
func TestSigV4SigningKey(t *testing.T) {
	key := deriveSigningKey(
		"wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		"20150830", "us-east-1", "iam",
	)
	const want = "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"
	if got := hex.EncodeToString(key); got != want {
		t.Fatalf("signing key = %s, want %s", got, want)
	}
}

func TestS3ConfigValidation(t *testing.T) {
	if _, err := NewS3(S3Config{}); err == nil {
		t.Fatal("empty S3 config should error")
	}
	s, err := NewS3(S3Config{Endpoint: "http://localhost:9000", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if s.region != "us-east-1" {
		t.Fatalf("default region = %q", s.region)
	}
	if s.Kind() != "s3" {
		t.Fatalf("kind = %q", s.Kind())
	}
}

func TestS3URIEncode(t *testing.T) {
	// Spaces and reserved chars are percent-encoded; slashes preserved when asked.
	if got := s3URIEncode("a b/c", false); got != "a%20b/c" {
		t.Fatalf("got %q", got)
	}
	if got := s3URIEncode("a/b", true); got != "a%2Fb" {
		t.Fatalf("got %q", got)
	}
}
