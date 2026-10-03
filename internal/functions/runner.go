package functions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// Invocation is a single function call's inputs.
type Invocation struct {
	Stdin []byte            // request body, delivered on the guest's stdin
	Env   map[string]string // the ONLY environment the guest sees (context + secrets)
	Args  []string          // argv (the function slug)
}

// Output is a single function call's result.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode uint32
}

// Runner executes a compiled WASM module for an invocation.
type Runner interface {
	// Invoke runs the module (id, etag); on a cache miss it calls load() to fetch
	// the wasm bytes, compiles, and caches them. ctx carries the timeout.
	Invoke(ctx context.Context, id, etag string, load func() ([]byte, error), in Invocation) (Output, error)
	Close(ctx context.Context) error
}

// ErrModuleInvalid is returned when wasm bytes fail to compile.
var ErrModuleInvalid = errors.New("invalid wasm module")

const maxOutputBytes = 8 << 20 // cap guest stdout/stderr at 8 MiB

// Wazero is a wazero-backed Runner. It is a strict sandbox: WASI with no
// preopened filesystem, no network (wazero has none), no subprocess, and only the
// explicitly-provided environment. Compiled modules are cached per (id, etag).
type Wazero struct {
	mu       sync.Mutex
	rt       wazero.Runtime
	cache    map[string]cached // id -> compiled module (current etag)
	memPages uint32
}

type cached struct {
	etag string
	mod  wazero.CompiledModule
}

// NewWazero builds a runtime with a global memory cap (maxMemoryMB) and
// context-cancellation interruption, so a spinning guest is stopped at the
// invocation deadline.
func NewWazero(ctx context.Context, maxMemoryMB int) (*Wazero, error) {
	if maxMemoryMB <= 0 {
		maxMemoryMB = 128
	}
	pages := uint32(maxMemoryMB) * 16 // 1 page = 64 KiB → 16 pages/MiB
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(pages)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	return &Wazero{rt: rt, cache: map[string]cached{}, memPages: pages}, nil
}

func (w *Wazero) compiled(ctx context.Context, id, etag string, load func() ([]byte, error)) (wazero.CompiledModule, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.cache[id]; ok && c.etag == etag {
		return c.mod, nil
	}
	bytesWasm, err := load()
	if err != nil {
		return nil, err
	}
	mod, err := w.rt.CompileModule(ctx, bytesWasm)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrModuleInvalid, err)
	}
	if old, ok := w.cache[id]; ok {
		_ = old.mod.Close(ctx)
	}
	w.cache[id] = cached{etag: etag, mod: mod}
	return mod, nil
}

func (w *Wazero) Invoke(ctx context.Context, id, etag string, load func() ([]byte, error), in Invocation) (Output, error) {
	mod, err := w.compiled(ctx, id, etag, load)
	if err != nil {
		return Output{}, err
	}

	stdout := &limitedBuffer{limit: maxOutputBytes}
	stderr := &limitedBuffer{limit: maxOutputBytes}
	cfg := wazero.NewModuleConfig().
		WithName(""). // anonymous → concurrent instances allowed
		WithStdin(bytes.NewReader(in.Stdin)).
		WithStdout(stdout).
		WithStderr(stderr).
		WithArgs(in.Args...)
	// Inject ONLY the explicit environment — never the host's os.Environ().
	for k, v := range in.Env {
		cfg = cfg.WithEnv(k, v)
	}
	// Deliberately no WithFSConfig → the guest has no filesystem access.

	instance, err := w.rt.InstantiateModule(ctx, mod, cfg)
	if instance != nil {
		_ = instance.Close(ctx)
	}
	out := Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err != nil {
		var exit *sys.ExitError
		if errors.As(err, &exit) {
			out.ExitCode = exit.ExitCode()
			return out, nil // a non-zero exit is a function-level result, not a host error
		}
		return out, err // context deadline, OOM trap, etc.
	}
	return out, nil
}

func (w *Wazero) Close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rt.Close(ctx)
}

// limitedBuffer is an io.Writer that keeps at most `limit` bytes and silently
// drops the rest, so a runaway guest cannot exhaust host memory via stdout.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil // report full consumption so the guest isn't blocked
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

var _ io.Writer = (*limitedBuffer)(nil)
