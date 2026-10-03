package functions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/blob"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

var (
	guestOnce sync.Once
	guestPath string
	guestErr  error
)

// guestWasm compiles the WASI test guest once per run. Tests skip if the Go
// wasip1 target is unavailable in this environment.
func guestWasm(t *testing.T) []byte {
	t.Helper()
	guestOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ps-guest-*")
		if err != nil {
			guestErr = err
			return
		}
		out := filepath.Join(dir, "guest.wasm")
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Dir = "testdata/guest"
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			guestErr = errors.New("compile guest: " + err.Error() + ": " + string(b))
			return
		}
		guestPath = out
	})
	if guestErr != nil {
		t.Skipf("wasip1 guest unavailable: %v", guestErr)
	}
	b, err := os.ReadFile(guestPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newFnStack(t *testing.T) (*Service, *Wazero) {
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
	runner, err := NewWazero(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(ctx) })
	return New(eng, store, runner), runner
}

var (
	admin = principal.Principal{Roles: []string{principal.RoleProjectAdmin}, Subject: "admin"}
	user  = principal.Principal{Roles: []string{principal.RoleAuthenticated}, Subject: "user-1"}
	anon  = principal.Anon()
)

func deploy(t *testing.T, s *Service, in CreateInput, wasm []byte) {
	t.Helper()
	if _, err := s.Create(context.Background(), in); err != nil {
		t.Fatalf("create %s: %v", in.Slug, err)
	}
	if _, err := s.UploadCode(context.Background(), in.Slug, strings.NewReader(string(wasm))); err != nil {
		t.Fatalf("upload %s: %v", in.Slug, err)
	}
}

func TestInvokeEchoSecretsAndSandbox(t *testing.T) {
	wasm := guestWasm(t)
	s, _ := newFnStack(t)
	deploy(t, s, CreateInput{
		Slug:        "greet",
		InvokeRoles: []string{principal.RoleAuthenticated},
		Secrets:     map[string]string{"GREETING": "hello"},
	}, wasm)

	out, err := s.Invoke(context.Background(), user, "greet", []byte("world"), InvokeMeta{Method: "POST"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", out.ExitCode, out.Stderr)
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Stdout, &resp); err != nil {
		t.Fatalf("bad guest output %q: %v", out.Stdout, err)
	}
	if resp["echo"] != "world" {
		t.Errorf("echo = %v, want world", resp["echo"])
	}
	if resp["greeting"] != "hello" {
		t.Errorf("secret injection failed: greeting = %v", resp["greeting"])
	}
	if resp["subject"] != "user-1" {
		t.Errorf("principal subject = %v, want user-1", resp["subject"])
	}
	if resp["method"] != "POST" {
		t.Errorf("method = %v, want POST", resp["method"])
	}
	if resp["fs"] != "denied" {
		t.Errorf("sandbox breach: filesystem = %v, want denied", resp["fs"])
	}
	if resp["host_env"] != "clean" {
		t.Errorf("host env leaked into guest: %v", resp["host_env"])
	}
}

func TestInvokeAuthorization(t *testing.T) {
	wasm := guestWasm(t)
	s, _ := newFnStack(t)
	deploy(t, s, CreateInput{Slug: "authed", InvokeRoles: []string{principal.RoleAuthenticated}}, wasm)

	if _, err := s.Invoke(context.Background(), anon, "authed", nil, InvokeMeta{}); !errors.Is(err, ErrDenied) {
		t.Fatalf("anon invoke should be denied, got %v", err)
	}
	if _, err := s.Invoke(context.Background(), user, "authed", nil, InvokeMeta{}); err != nil {
		t.Fatalf("authenticated invoke should be allowed: %v", err)
	}
	if _, err := s.Invoke(context.Background(), admin, "authed", nil, InvokeMeta{}); err != nil {
		t.Fatalf("admin invoke should always be allowed: %v", err)
	}

	// admin-only function (empty invoke_roles): non-admin denied, admin allowed.
	deploy(t, s, CreateInput{Slug: "adminonly", InvokeRoles: []string{}}, wasm)
	if _, err := s.Invoke(context.Background(), user, "adminonly", nil, InvokeMeta{}); !errors.Is(err, ErrDenied) {
		t.Fatalf("user invoke of admin-only fn should be denied, got %v", err)
	}
	if _, err := s.Invoke(context.Background(), admin, "adminonly", nil, InvokeMeta{}); err != nil {
		t.Fatalf("admin invoke of admin-only fn: %v", err)
	}
}

func TestInvokeTimeout(t *testing.T) {
	wasm := guestWasm(t)
	s, _ := newFnStack(t)
	deploy(t, s, CreateInput{
		Slug:        "spin",
		TimeoutMS:   400,
		InvokeRoles: []string{principal.RoleAuthenticated},
		Secrets:     map[string]string{"MODE": "spin"},
	}, wasm)

	start := time.Now()
	_, err := s.Invoke(context.Background(), user, "spin", nil, InvokeMeta{})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("spin invoke should time out, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took too long (%s) — not interrupting the busy loop", elapsed)
	}
}

func TestInvokeNonZeroExit(t *testing.T) {
	wasm := guestWasm(t)
	s, _ := newFnStack(t)
	deploy(t, s, CreateInput{
		Slug:        "fail",
		InvokeRoles: []string{principal.RoleAuthenticated},
		Secrets:     map[string]string{"MODE": "fail"},
	}, wasm)

	out, err := s.Invoke(context.Background(), user, "fail", nil, InvokeMeta{})
	if err != nil {
		t.Fatalf("non-zero exit is a result, not a host error: %v", err)
	}
	if out.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "boom") {
		t.Errorf("stderr not captured: %q", out.Stderr)
	}
}

func TestMetadataAndLifecycle(t *testing.T) {
	s, _ := newFnStack(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, CreateInput{Slug: "Bad Slug"}); !errors.Is(err, ErrInvalidSlug) {
		t.Fatalf("invalid slug should be rejected, got %v", err)
	}
	if _, err := s.Create(ctx, CreateInput{Slug: "dup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateInput{Slug: "dup"}); !errors.Is(err, ErrFunctionExists) {
		t.Fatalf("duplicate slug should be rejected, got %v", err)
	}
	// secret values are never surfaced in metadata — only names.
	if _, err := s.Create(ctx, CreateInput{Slug: "withsecret", Secrets: map[string]string{"TOKEN": "s3cr3t"}}); err != nil {
		t.Fatal(err)
	}
	f, err := s.Get(ctx, "withsecret")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.SecretNames) != 1 || f.SecretNames[0] != "TOKEN" {
		t.Fatalf("secret names = %v, want [TOKEN]", f.SecretNames)
	}
	if f.HasCode {
		t.Fatal("function should have no code before upload")
	}
	// invoking a function with no code → ErrNoCode.
	if _, err := s.Invoke(ctx, admin, "withsecret", nil, InvokeMeta{}); !errors.Is(err, ErrNoCode) {
		t.Fatalf("invoke without code should be ErrNoCode, got %v", err)
	}
	if err := s.Delete(ctx, "dup"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "dup"); !errors.Is(err, ErrFunctionNotFound) {
		t.Fatalf("deleted function should be gone, got %v", err)
	}
}

func TestInvalidModuleRejected(t *testing.T) {
	s, _ := newFnStack(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateInput{Slug: "garbage", InvokeRoles: []string{principal.RoleAnon}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCode(ctx, "garbage", strings.NewReader("not wasm at all")); err != nil {
		t.Fatalf("upload stores bytes without compiling: %v", err)
	}
	// compilation happens on first invoke → ErrInvalidModule.
	if _, err := s.Invoke(ctx, anon, "garbage", nil, InvokeMeta{}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("invoking a non-wasm module should be ErrInvalidModule, got %v", err)
	}
}
