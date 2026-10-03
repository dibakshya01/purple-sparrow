// Package functions is M7: edge functions as sandboxed WASM (WASI) modules run
// by wazero in-process. A function reads its request body on stdin and writes its
// response on stdout; invocation context and the function's explicit secrets are
// injected as environment variables — and nothing else, so the guest never sees
// the host's environment. The sandbox has no filesystem, no network, and no
// subprocess; a per-invocation timeout and a memory cap bound resource use.
//
// Module bytes live in the BlobStore (keyed by function id, reusing M6). This
// package owns metadata, invocation authorization, and the runtime wiring.
package functions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/blob"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

// Sentinel errors mapped to envelope codes by the HTTP layer.
var (
	ErrFunctionNotFound = errors.New("function not found")
	ErrFunctionExists   = errors.New("function already exists")
	ErrInvalidSlug      = errors.New("invalid function slug")
	ErrInvalidConfig    = errors.New("invalid function config")
	ErrNoCode           = errors.New("function has no code")
	ErrDenied           = errors.New("not authorized to invoke")
	ErrTimeout          = errors.New("function timed out")
	ErrInvalidModule    = errors.New("invalid wasm module")
)

const (
	defaultTimeoutMS = 5000
	maxTimeoutMS     = 60000
	defaultMemoryMB  = 64       // recorded in the metadata column; not a per-function cap
	maxWasmBytes     = 32 << 20 // 32 MiB module cap
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// Function is a deployed function's metadata (secret *values* are never exposed).
//
// Note: memory is a process-wide cap (PS_FN_MAX_MEMORY_MB), not per-function —
// wazero's memory limit is per-runtime — so no per-function memory field is
// exposed, to avoid advertising isolation that does not exist.
type Function struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Runtime     string   `json:"runtime"`
	TimeoutMS   int      `json:"timeout_ms"`
	InvokeRoles []string `json:"invoke_roles"`
	SecretNames []string `json:"secret_names"`
	HasCode     bool     `json:"has_code"`
	ETag        string   `json:"etag,omitempty"`
	Size        int64    `json:"size"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// CreateInput is the payload for deploying function metadata.
type CreateInput struct {
	Slug        string            `json:"slug"`
	TimeoutMS   int               `json:"timeout_ms"`
	InvokeRoles []string          `json:"invoke_roles"`
	Secrets     map[string]string `json:"secrets"`
}

// Service owns function metadata, code storage, and invocation.
type Service struct {
	eng    data.Engine
	store  blob.Store
	runner Runner
}

// New returns a functions Service.
func New(eng data.Engine, store blob.Store, runner Runner) *Service {
	return &Service{eng: eng, store: store, runner: runner}
}

// Create deploys function metadata (admin). Code is uploaded separately.
func (s *Service) Create(ctx context.Context, in CreateInput) (Function, error) {
	if !slugRe.MatchString(in.Slug) {
		return Function{}, fmt.Errorf("%w: slug must be 2-63 chars, lowercase alphanumeric with -", ErrInvalidSlug)
	}
	if existing, _ := s.getBySlug(ctx, in.Slug); existing != nil {
		return Function{}, ErrFunctionExists
	}
	for _, r := range in.InvokeRoles {
		switch r {
		case principal.RoleAnon, principal.RoleAuthenticated, principal.RoleProjectAdmin:
		default:
			return Function{}, fmt.Errorf("%w: invoke role %q is not one of anon, authenticated, project_admin", ErrInvalidConfig, r)
		}
	}
	timeout := clamp(in.TimeoutMS, defaultTimeoutMS, 1, maxTimeoutMS)
	// memory_mb is recorded (column default) but not a per-function cap; see Function.
	memory := defaultMemoryMB
	if in.Secrets == nil {
		in.Secrets = map[string]string{}
	}
	if in.InvokeRoles == nil {
		in.InvokeRoles = []string{}
	}
	roles, _ := json.Marshal(in.InvokeRoles)
	secrets, _ := json.Marshal(in.Secrets)
	now := nowISO()
	id := idgen.NewUUID()
	_, err := s.eng.ExecCtx(ctx,
		`INSERT INTO _ps_functions (id, slug, runtime, timeout_ms, memory_mb, invoke_roles, secrets, etag, size, has_code, created_at, updated_at)
		 VALUES (?, ?, 'wasi', ?, ?, ?, ?, '', 0, 0, ?, ?)`,
		id, in.Slug, timeout, memory, string(roles), string(secrets), now, now)
	if err != nil {
		return Function{}, err
	}
	f, err := s.getBySlug(ctx, in.Slug)
	if err != nil {
		return Function{}, err
	}
	return *f, nil
}

// UploadCode stores (or replaces) a function's wasm module. Admin-only.
func (s *Service) UploadCode(ctx context.Context, slug string, r io.Reader) (Function, error) {
	f, err := s.getBySlug(ctx, slug)
	if err != nil {
		return Function{}, err
	}
	wasm, err := io.ReadAll(io.LimitReader(r, maxWasmBytes+1))
	if err != nil {
		return Function{}, err
	}
	if len(wasm) == 0 {
		return Function{}, fmt.Errorf("%w: empty module", ErrInvalidModule)
	}
	if len(wasm) > maxWasmBytes {
		return Function{}, fmt.Errorf("%w: module exceeds %d bytes", ErrInvalidModule, maxWasmBytes)
	}
	size, etag, err := s.store.Put(ctx, f.ID, strings.NewReader(string(wasm)))
	if err != nil {
		return Function{}, err
	}
	now := nowISO()
	if _, err := s.eng.ExecCtx(ctx,
		`UPDATE _ps_functions SET etag=?, size=?, has_code=1, updated_at=? WHERE id=?`,
		etag, size, now, f.ID); err != nil {
		return Function{}, err
	}
	return *mustGet(s, ctx, slug), nil
}

// List returns all functions (admin).
func (s *Service) List(ctx context.Context) ([]Function, error) {
	rows, err := s.eng.QueryCtx(ctx, fnCols+` ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	out := make([]Function, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToFunction(r))
	}
	return out, nil
}

// Get returns a function's metadata (admin).
func (s *Service) Get(ctx context.Context, slug string) (Function, error) {
	f, err := s.getBySlug(ctx, slug)
	if err != nil {
		return Function{}, err
	}
	return *f, nil
}

// Delete removes a function and its code (admin).
func (s *Service) Delete(ctx context.Context, slug string) error {
	f, err := s.getBySlug(ctx, slug)
	if err != nil {
		return err
	}
	if _, err := s.eng.ExecCtx(ctx, `DELETE FROM _ps_functions WHERE id=?`, f.ID); err != nil {
		return err
	}
	if f.HasCode {
		_ = s.store.Delete(ctx, f.ID)
	}
	return nil
}

// InvokeMeta is the request context passed to a function as environment.
type InvokeMeta struct {
	Method string
	Query  string
}

// Invoke authorizes and runs a function. Returns the module output; a non-zero
// Output.ExitCode is a function-level failure (the caller maps it to 5xx).
func (s *Service) Invoke(ctx context.Context, p principal.Principal, slug string, body []byte, meta InvokeMeta) (Output, error) {
	f, secrets, err := s.getWithSecrets(ctx, slug)
	if err != nil {
		return Output{}, err
	}
	if !f.HasCode {
		return Output{}, ErrNoCode
	}
	if !authorized(p, f.InvokeRoles) {
		return Output{}, ErrDenied
	}

	env := map[string]string{}
	for k, v := range secrets {
		env[k] = v
	}
	// Context vars use the reserved PS_ prefix and are set last so a secret cannot
	// shadow them. Admin-deployed functions are trusted, but this keeps it tidy.
	env["PS_METHOD"] = meta.Method
	env["PS_QUERY"] = meta.Query
	env["PS_PRINCIPAL_SUBJECT"] = p.Subject
	env["PS_PRINCIPAL_ROLES"] = strings.Join(p.Roles, ",")

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(f.TimeoutMS)*time.Millisecond)
	defer cancel()

	out, err := s.runner.Invoke(runCtx, f.ID, f.ETag,
		func() ([]byte, error) { return s.loadCode(ctx, f.ID) },
		Invocation{Stdin: body, Env: env, Args: []string{f.Slug}})
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return Output{}, ErrTimeout
		}
		if errors.Is(err, ErrModuleInvalid) {
			return Output{}, fmt.Errorf("%w: %v", ErrInvalidModule, err)
		}
		return Output{}, err
	}
	return out, nil
}

func (s *Service) loadCode(ctx context.Context, id string) ([]byte, error) {
	rc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxWasmBytes))
}

// --- helpers --------------------------------------------------------------

const fnCols = `SELECT id, slug, runtime, timeout_ms, memory_mb, invoke_roles, secrets, etag, size, has_code, created_at, updated_at FROM _ps_functions`

func (s *Service) getBySlug(ctx context.Context, slug string) (*Function, error) {
	row, err := s.eng.QueryRowCtx(ctx, fnCols+` WHERE slug = ?`, slug)
	if errors.Is(err, data.ErrNoRows) || row == nil {
		return nil, ErrFunctionNotFound
	}
	if err != nil {
		return nil, err
	}
	f := rowToFunction(row)
	return &f, nil
}

func (s *Service) getWithSecrets(ctx context.Context, slug string) (*Function, map[string]string, error) {
	row, err := s.eng.QueryRowCtx(ctx, fnCols+` WHERE slug = ?`, slug)
	if errors.Is(err, data.ErrNoRows) || row == nil {
		return nil, nil, ErrFunctionNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	f := rowToFunction(row)
	secrets := map[string]string{}
	_ = json.Unmarshal([]byte(str(row["secrets"])), &secrets)
	return &f, secrets, nil
}

func mustGet(s *Service, ctx context.Context, slug string) *Function {
	f, _ := s.getBySlug(ctx, slug)
	if f == nil {
		return &Function{}
	}
	return f
}

func rowToFunction(r data.Row) Function {
	var roles []string
	_ = json.Unmarshal([]byte(str(r["invoke_roles"])), &roles)
	if roles == nil {
		roles = []string{}
	}
	var secrets map[string]string
	_ = json.Unmarshal([]byte(str(r["secrets"])), &secrets)
	names := make([]string, 0, len(secrets))
	for k := range secrets {
		names = append(names, k)
	}
	return Function{
		ID:          str(r["id"]),
		Slug:        str(r["slug"]),
		Runtime:     str(r["runtime"]),
		TimeoutMS:   int(asInt(r["timeout_ms"])),
		InvokeRoles: roles,
		SecretNames: names,
		HasCode:     asInt(r["has_code"]) == 1,
		ETag:        str(r["etag"]),
		Size:        asInt(r["size"]),
		CreatedAt:   str(r["created_at"]),
		UpdatedAt:   str(r["updated_at"]),
	}
}

func authorized(p principal.Principal, invokeRoles []string) bool {
	if p.IsAdmin() {
		return true
	}
	return len(invokeRoles) > 0 && p.IntersectsRoles(invokeRoles)
}

func clamp(v, def, lo, hi int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func nowISO() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
