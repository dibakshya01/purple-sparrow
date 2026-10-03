package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	"github.com/dibakshya01/purple-sparrow/internal/functions"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
)

var (
	fnGuestOnce sync.Once
	fnGuest     []byte
	fnGuestErr  error
)

func buildGuest(t *testing.T) []byte {
	t.Helper()
	fnGuestOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ps-fn-e2e-*")
		if err != nil {
			fnGuestErr = err
			return
		}
		out := filepath.Join(dir, "guest.wasm")
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Dir = filepath.FromSlash("../functions/testdata/guest")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			fnGuestErr = errors.New(err.Error() + ": " + string(b))
			return
		}
		fnGuest, fnGuestErr = os.ReadFile(out)
	})
	if fnGuestErr != nil {
		t.Skipf("wasip1 guest unavailable: %v", fnGuestErr)
	}
	return fnGuest
}

func fnServer(t *testing.T) *Server {
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
	cat := catalog.New(eng)
	pol := policy.NewService(eng)
	enf := policy.NewEnforcer(eng)
	authSvc, err := auth.NewService(ctx, eng)
	if err != nil {
		t.Fatal(err)
	}
	if err := authSvc.SeedAdminKey(ctx, testAdminKey); err != nil {
		t.Fatal(err)
	}
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := functions.NewWazero(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(ctx) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Defaults(), logger, Deps{
		Catalog: cat, Records: records.New(eng, cat, enf), Policy: pol,
		Meta: meta.New(eng, cat, pol), Auth: authSvc,
		Docs: agentdocs.New(), Memory: memory.New(eng), Advisor: advisor.New(cat, pol),
		Functions: functions.New(eng, store, runner),
	})
}

func TestE2E_Functions(t *testing.T) {
	wasm := buildGuest(t)
	s := fnServer(t)

	// non-admin cannot deploy
	if rec := do(t, s, "POST", "/v1/functions", "", `{"slug":"echo"}`); rec.Code != 403 {
		t.Fatalf("anon deploy: want 403, got %d", rec.Code)
	}
	// admin deploys metadata (anon-invocable, with a secret)
	if rec := do(t, s, "POST", "/v1/functions", testAdminKey,
		`{"slug":"echo","invoke_roles":["anon"],"secrets":{"GREETING":"hi"}}`); rec.Code != 201 {
		t.Fatalf("deploy: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	// invoking before code is uploaded → 409
	if rec := do(t, s, "POST", "/v1/fn/echo", "", "x"); rec.Code != 409 {
		t.Fatalf("invoke no-code: want 409, got %d", rec.Code)
	}
	// upload the wasm module (raw body)
	rec := do(t, s, "PUT", "/v1/functions/echo/code", testAdminKey, string(wasm))
	if rec.Code != 200 {
		t.Fatalf("upload code: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// invoke as anon (allowed by invoke_roles) with a body
	rec = do(t, s, "POST", "/v1/fn/echo", "", "payload")
	if rec.Code != 200 {
		t.Fatalf("invoke: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("guest output not JSON: %q", rec.Body.String())
	}
	if resp["echo"] != "payload" || resp["greeting"] != "hi" {
		t.Fatalf("unexpected guest output: %v", resp)
	}
	if resp["fs"] != "denied" || resp["host_env"] != "clean" {
		t.Fatalf("sandbox not enforced: %v", resp)
	}

	// list shows the function; metadata never leaks secret values
	rec = do(t, s, "GET", "/v1/functions", testAdminKey, "")
	if rec.Code != 200 {
		t.Fatalf("list: %d", rec.Code)
	}
	if got := rec.Body.String(); strings.Contains(got, `"GREETING"`) || strings.Contains(got, `"hi"`) {
		t.Fatalf("secret value leaked in list output: %s", got)
	}
}
