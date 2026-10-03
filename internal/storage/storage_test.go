package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/blob"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	eng, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := migrate.Run(ctx, eng); err != nil {
		t.Fatal(err)
	}
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(ctx, eng, store)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

var (
	admin = principal.Principal{Roles: []string{principal.RoleProjectAdmin}, Subject: "admin"}
	userA = principal.Principal{Roles: []string{principal.RoleAuthenticated}, Subject: "user-a"}
	userB = principal.Principal{Roles: []string{principal.RoleAuthenticated}, Subject: "user-b"}
	anon  = principal.Anon()
)

func put(t *testing.T, s *Service, p principal.Principal, bucket, key, body string) Object {
	t.Helper()
	obj, err := s.Put(context.Background(), p, bucket, key, "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatalf("put %s/%s: %v", bucket, key, err)
	}
	return obj
}

func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBucketLifecycle(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "Photos", false); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("uppercase bucket name should be rejected, got %v", err)
	}
	if _, err := s.CreateBucket(ctx, "photos", false); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if _, err := s.CreateBucket(ctx, "photos", false); !errors.Is(err, ErrBucketExists) {
		t.Fatalf("duplicate bucket should be rejected, got %v", err)
	}
	bs, err := s.ListBuckets(ctx)
	if err != nil || len(bs) != 1 || bs[0].Name != "photos" {
		t.Fatalf("list buckets: %v %+v", err, bs)
	}
}

func TestObjectOwnershipAndContent(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "private", false); err != nil {
		t.Fatal(err)
	}

	// anon cannot write
	if _, err := s.Put(ctx, anon, "private", "a.txt", "text/plain", strings.NewReader("x")); !errors.Is(err, ErrAnonWrite) {
		t.Fatalf("anon write should be denied, got %v", err)
	}

	obj := put(t, s, userA, "private", "a.txt", "hello world")
	if obj.Size != 11 || obj.OwnerID != "user-a" {
		t.Fatalf("unexpected object meta: %+v", obj)
	}

	// owner reads bytes back
	got, rc, err := s.Get(ctx, userA, "private", "a.txt")
	if err != nil {
		t.Fatalf("owner get: %v", err)
	}
	if body := readAll(t, rc); body != "hello world" {
		t.Fatalf("content mismatch: %q", body)
	}
	if got.ETag == "" {
		t.Fatal("etag should be set")
	}

	// other user denied; admin allowed; anon denied on private
	if _, _, err := s.Get(ctx, userB, "private", "a.txt"); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-user read should be denied, got %v", err)
	}
	if _, _, err := s.Get(ctx, admin, "private", "a.txt"); err != nil {
		t.Fatalf("admin read should be allowed, got %v", err)
	}
	if _, _, err := s.Get(ctx, anon, "private", "a.txt"); !errors.Is(err, ErrDenied) {
		t.Fatalf("anon read of private should be denied, got %v", err)
	}
}

func TestPublicBucketReadableByAnyone(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "public", true); err != nil {
		t.Fatal(err)
	}
	put(t, s, userA, "public", "logo.png", "PNGDATA")
	if _, rc, err := s.Get(ctx, anon, "public", "logo.png"); err != nil {
		t.Fatalf("anon read of public should be allowed: %v", err)
	} else {
		rc.Close()
	}
}

func TestOverwriteRequiresOwner(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "bk", false); err != nil {
		t.Fatal(err)
	}
	first := put(t, s, userA, "bk", "k", "v1")

	// userB cannot overwrite userA's object
	if _, err := s.Put(ctx, userB, "bk", "k", "text/plain", strings.NewReader("evil")); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-user overwrite should be denied, got %v", err)
	}
	// owner overwrite reuses the same id and preserves owner
	second := put(t, s, userA, "bk", "k", "v2-longer")
	if second.ID != first.ID {
		t.Fatalf("overwrite should reuse id: %s != %s", second.ID, first.ID)
	}
	if second.OwnerID != "user-a" {
		t.Fatalf("overwrite should preserve owner, got %q", second.OwnerID)
	}
	_, rc, err := s.Get(ctx, userA, "bk", "k")
	if err != nil {
		t.Fatal(err)
	}
	if body := readAll(t, rc); body != "v2-longer" {
		t.Fatalf("overwrite content wrong: %q", body)
	}
}

func TestListIsOwnerScoped(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "bk", false); err != nil {
		t.Fatal(err)
	}
	put(t, s, userA, "bk", "a1", "x")
	put(t, s, userA, "bk", "a2", "x")
	put(t, s, userB, "bk", "b1", "x")

	aList, _ := s.List(ctx, userA, "bk")
	if len(aList) != 2 {
		t.Fatalf("user A should see 2 objects, saw %d", len(aList))
	}
	bList, _ := s.List(ctx, userB, "bk")
	if len(bList) != 1 {
		t.Fatalf("user B should see 1 object, saw %d", len(bList))
	}
	adminList, _ := s.List(ctx, admin, "bk")
	if len(adminList) != 3 {
		t.Fatalf("admin should see 3 objects, saw %d", len(adminList))
	}
	anonList, _ := s.List(ctx, anon, "bk")
	if len(anonList) != 0 {
		t.Fatalf("anon should see 0 objects, saw %d", len(anonList))
	}
}

func TestDeleteRequiresOwner(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "bk", false); err != nil {
		t.Fatal(err)
	}
	put(t, s, userA, "bk", "k", "v")
	if err := s.Delete(ctx, userB, "bk", "k"); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-user delete should be denied, got %v", err)
	}
	if err := s.Delete(ctx, userA, "bk", "k"); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, _, err := s.Get(ctx, userA, "bk", "k"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("object should be gone, got %v", err)
	}
}

func TestPresign(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "bk", false); err != nil {
		t.Fatal(err)
	}
	put(t, s, userA, "bk", "k", "secret-bytes")

	// non-owner cannot presign
	if _, _, err := s.Presign(ctx, userB, "bk", "k", time.Minute); !errors.Is(err, ErrDenied) {
		t.Fatalf("non-owner presign should be denied, got %v", err)
	}

	path, _, err := s.Presign(ctx, userA, "bk", "k", time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	// extract exp & sig from the returned path
	exp, sig := parseSigned(t, path)

	// valid signature grants access with no principal
	_, rc, err := s.GetSigned(ctx, "bk", "k", exp, sig)
	if err != nil {
		t.Fatalf("valid signed get: %v", err)
	}
	if body := readAll(t, rc); body != "secret-bytes" {
		t.Fatalf("signed content wrong: %q", body)
	}

	// tampered signature denied
	if _, _, err := s.GetSigned(ctx, "bk", "k", exp, sig+"00"); !errors.Is(err, ErrDenied) {
		t.Fatalf("tampered sig should be denied, got %v", err)
	}
	// changing the key under the same sig is denied
	if _, _, err := s.GetSigned(ctx, "bk", "other", exp, sig); err == nil {
		t.Fatal("sig bound to a different key should not grant access")
	}
	// expired signature denied (sign for a past expiry directly)
	pastExp := time.Now().Add(-time.Minute).Unix()
	if _, _, err := s.GetSigned(ctx, "bk", "k", pastExp, s.sign("bk", "k", pastExp)); !errors.Is(err, ErrDenied) {
		t.Fatalf("expired sig should be denied, got %v", err)
	}
}

func TestDeleteBucketRemovesObjects(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "bk", false); err != nil {
		t.Fatal(err)
	}
	put(t, s, userA, "bk", "k", "v")
	if err := s.DeleteBucket(ctx, "bk"); err != nil {
		t.Fatalf("delete bucket: %v", err)
	}
	if _, err := s.List(ctx, admin, "bk"); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("bucket should be gone, got %v", err)
	}
}

func TestKeyValidation(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, "bk", false); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "../etc/passwd", "a//b", "/leading"} {
		if _, err := s.Put(ctx, userA, "bk", bad, "text/plain", strings.NewReader("x")); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("key %q should be rejected, got %v", bad, err)
		}
	}
	// a nested, legitimate key is fine
	if _, err := s.Put(ctx, userA, "bk", "a/b/c.txt", "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("nested key should be allowed: %v", err)
	}
}

func parseSigned(t *testing.T, path string) (int64, string) {
	t.Helper()
	i := strings.Index(path, "?")
	if i < 0 {
		t.Fatalf("no query in %q", path)
	}
	var exp int64
	var sig string
	for _, kv := range strings.Split(path[i+1:], "&") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "exp":
			_, _ = sscanInt(v, &exp)
		case "sig":
			sig = v
		}
	}
	if sig == "" || exp == 0 {
		t.Fatalf("missing exp/sig in %q", path)
	}
	return exp, sig
}

func sscanInt(s string, out *int64) (int, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("nan")
		}
		n = n*10 + int64(c-'0')
	}
	*out = n
	return 1, nil
}

// (bucket "bk" is used throughout; single-char names are below the 2-char minimum)
