package httpapi

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/agent/advisor"
	agentdocs "github.com/dibakshya01/purple-sparrow/internal/agent/docs"
	"github.com/dibakshya01/purple-sparrow/internal/agent/memory"
	"github.com/dibakshya01/purple-sparrow/internal/agent/meta"
	"github.com/dibakshya01/purple-sparrow/internal/auth"
	"github.com/dibakshya01/purple-sparrow/internal/blob"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
	"github.com/dibakshya01/purple-sparrow/internal/storage"
)

func storageServer(t *testing.T, maxObjectBytes int64) *Server {
	t.Helper()
	eng, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := migrate.Run(context.Background(), eng); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(eng)
	pol := policy.NewService(eng)
	enf := policy.NewEnforcer(eng)
	authSvc, err := auth.NewService(context.Background(), eng)
	if err != nil {
		t.Fatal(err)
	}
	if err := authSvc.SeedAdminKey(context.Background(), testAdminKey); err != nil {
		t.Fatal(err)
	}
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stg, err := storage.New(context.Background(), eng, store)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.StorageMaxObjectBytes = maxObjectBytes
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, logger, Deps{
		Catalog: cat, Records: records.New(eng, cat, enf), Policy: pol,
		Meta: meta.New(eng, cat, pol), Auth: authSvc,
		Docs: agentdocs.New(), Memory: memory.New(eng), Advisor: advisor.New(cat, pol),
		Storage: stg,
	})
}

func TestE2E_Storage(t *testing.T) {
	s := storageServer(t, 100<<20)

	// anon cannot create a bucket
	if rec := do(t, s, "POST", "/v1/storage/buckets", "", `{"name":"assets"}`); rec.Code != 403 {
		t.Fatalf("anon create bucket: want 403, got %d", rec.Code)
	}
	// admin creates a public bucket
	if rec := do(t, s, "POST", "/v1/storage/buckets", testAdminKey, `{"name":"assets","public":true}`); rec.Code != 201 {
		t.Fatalf("admin create bucket: want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// admin uploads an object (raw bytes)
	rec := do(t, s, "PUT", "/v1/storage/assets/logo.txt", testAdminKey, "PNG-BYTES-HERE")
	if rec.Code != 201 {
		t.Fatalf("upload: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	obj := decode(t, rec)
	if obj["size"].(float64) != 14 {
		t.Fatalf("size = %v, want 14", obj["size"])
	}

	// download returns the exact bytes
	rec = do(t, s, "GET", "/v1/storage/assets/logo.txt", testAdminKey, "")
	if rec.Code != 200 || rec.Body.String() != "PNG-BYTES-HERE" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatal("missing content-type on download")
	}
	// object bytes must be served with a sandbox CSP so attacker-uploaded HTML/SVG
	// cannot run script on the API origin (stored-XSS defense).
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("object response must carry a sandbox CSP, got %q", csp)
	}

	// public bucket: anon can read
	if rec := do(t, s, "GET", "/v1/storage/assets/logo.txt", "", ""); rec.Code != 200 {
		t.Fatalf("anon read of public object: want 200, got %d", rec.Code)
	}

	// presign returns a signed URL that works with no credentials
	rec = do(t, s, "GET", "/v1/storage/assets/logo.txt?presign=120", testAdminKey, "")
	if rec.Code != 200 {
		t.Fatalf("presign: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	signedURL, _ := decode(t, rec)["url"].(string)
	if signedURL == "" {
		t.Fatal("presign returned no url")
	}
	if rec := do(t, s, "GET", signedURL, "", ""); rec.Code != 200 || rec.Body.String() != "PNG-BYTES-HERE" {
		t.Fatalf("signed GET: %d %q", rec.Code, rec.Body.String())
	}
	// tampering the signature fails
	if rec := do(t, s, "GET", signedURL+"deadbeef", "", ""); rec.Code == 200 {
		t.Fatal("tampered signed GET should not return 200")
	}

	// list objects (admin sees all)
	rec = do(t, s, "GET", "/v1/storage/assets", testAdminKey, "")
	if rec.Code != 200 {
		t.Fatalf("list: %d", rec.Code)
	}

	// delete
	if rec := do(t, s, "DELETE", "/v1/storage/assets/logo.txt", testAdminKey, ""); rec.Code != 204 {
		t.Fatalf("delete: want 204, got %d", rec.Code)
	}
	if rec := do(t, s, "GET", "/v1/storage/assets/logo.txt", testAdminKey, ""); rec.Code != 404 {
		t.Fatalf("get after delete: want 404, got %d", rec.Code)
	}
}

func TestE2E_StorageObjectTooLarge(t *testing.T) {
	s := storageServer(t, 8) // 8-byte cap
	if rec := do(t, s, "POST", "/v1/storage/buckets", testAdminKey, `{"name":"small"}`); rec.Code != 201 {
		t.Fatalf("create bucket: %d", rec.Code)
	}
	rec := do(t, s, "PUT", "/v1/storage/small/big.bin", testAdminKey, "way-too-many-bytes")
	if rec.Code != 413 {
		t.Fatalf("oversize upload: want 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestE2E_StorageAnonPrivateDenied(t *testing.T) {
	s := storageServer(t, 100<<20)
	if rec := do(t, s, "POST", "/v1/storage/buckets", testAdminKey, `{"name":"vault","public":false}`); rec.Code != 201 {
		t.Fatalf("create bucket: %d", rec.Code)
	}
	if rec := do(t, s, "PUT", "/v1/storage/vault/secret.txt", testAdminKey, "classified"); rec.Code != 201 {
		t.Fatalf("admin upload: %d", rec.Code)
	}
	// anon read of a private object → 403
	if rec := do(t, s, "GET", "/v1/storage/vault/secret.txt", "", ""); rec.Code != 403 {
		t.Fatalf("anon read of private: want 403, got %d", rec.Code)
	}
}
