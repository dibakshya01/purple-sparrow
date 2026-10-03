package httpapi

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/agent/advisor"
	agentdocs "github.com/dibakshya01/purple-sparrow/internal/agent/docs"
	"github.com/dibakshya01/purple-sparrow/internal/agent/memory"
	"github.com/dibakshya01/purple-sparrow/internal/agent/meta"
	"github.com/dibakshya01/purple-sparrow/internal/auth"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/events"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
)

func realtimeServer(t *testing.T) (*Server, *events.Hub) {
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
	rec := records.New(eng, cat, enf)
	bus := events.NewHub()
	rec.SetPublisher(bus)
	authSvc, err := auth.NewService(ctx, eng)
	if err != nil {
		t.Fatal(err)
	}
	if err := authSvc.SeedAdminKey(ctx, testAdminKey); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(config.Defaults(), logger, Deps{
		Catalog: cat, Records: rec, Policy: pol,
		Meta: meta.New(eng, cat, pol), Auth: authSvc,
		Docs: agentdocs.New(), Memory: memory.New(eng), Advisor: advisor.New(cat, pol),
		Bus: bus, Enforcer: enf, Engine: eng,
	})
	return s, bus
}

// collectSSE opens an SSE connection and returns a channel of "data:" payloads.
func collectSSE(t *testing.T, ctx context.Context, url string) <-chan string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse connect: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("sse status %d", resp.StatusCode)
	}
	out := make(chan string, 32)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				select {
				case out <- data:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func TestE2E_RealtimePolicyFiltered(t *testing.T) {
	s, bus := realtimeServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// A public table (anon may select) and a private table (no anon policy).
	if rec := do(t, s, "POST", "/v1/tables", testAdminKey, `{"name":"pub","columns":[{"name":"body","type":"text"}]}`); rec.Code != 201 {
		t.Fatalf("create pub: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, "POST", "/v1/tables", testAdminKey, `{"name":"priv","columns":[{"name":"body","type":"text"}]}`); rec.Code != 201 {
		t.Fatalf("create priv: %d", rec.Code)
	}
	if rec := do(t, s, "POST", "/v1/policies", testAdminKey, `{"table":"pub","action":"select","roles":["anon"],"using":"true"}`); rec.Code != 201 {
		t.Fatalf("policy: %d %s", rec.Code, rec.Body.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// anon subscriber (no auth). Wait until it is registered before publishing.
	anonEvents := collectSSE(t, ctx, ts.URL+"/v1/realtime")
	waitForSubscribers(t, bus, 1)

	// admin inserts into both tables
	if rec := do(t, s, "POST", "/v1/tables/priv/records", testAdminKey, `{"body":"secret"}`); rec.Code != 201 {
		t.Fatalf("insert priv: %d", rec.Code)
	}
	if rec := do(t, s, "POST", "/v1/tables/pub/records", testAdminKey, `{"body":"hello"}`); rec.Code != 201 {
		t.Fatalf("insert pub: %d", rec.Code)
	}

	// The anon stream must see the pub insert and never the priv insert.
	got := drain(anonEvents, 1500*time.Millisecond)
	if !anyContains(got, `"table":"pub"`) {
		t.Fatalf("anon should have received the pub event; got %v", got)
	}
	if anyContains(got, `"table":"priv"`) || anyContains(got, "secret") {
		t.Fatalf("anon LEAKED a private event; got %v", got)
	}
}

func TestE2E_RealtimeAdminSeesAll(t *testing.T) {
	s, bus := realtimeServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	if rec := do(t, s, "POST", "/v1/tables", testAdminKey, `{"name":"priv","columns":[{"name":"body","type":"text"}]}`); rec.Code != 201 {
		t.Fatalf("create: %d", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	adminEvents := collectSSE(t, ctx, ts.URL+"/v1/realtime?table=priv")
	waitForSubscribers(t, bus, 1)
	// Note: the SSE connection above is anon; open an admin one too to prove admin sees all.
	_ = adminEvents

	// admin SSE
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/v1/realtime?table=priv", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin sse: %v", err)
	}
	defer resp.Body.Close()
	waitForSubscribers(t, bus, 2)

	if rec := do(t, s, "POST", "/v1/tables/priv/records", testAdminKey, `{"body":"x"}`); rec.Code != 201 {
		t.Fatalf("insert: %d", rec.Code)
	}

	// admin stream should receive the private insert
	adminGot := readSSEFor(resp.Body, 1500*time.Millisecond)
	if !strings.Contains(adminGot, `"table":"priv"`) {
		t.Fatalf("admin should receive private events; got %q", adminGot)
	}
	// the anon stream (filtered to priv) should NOT
	anonGot := drain(adminEvents, 200*time.Millisecond)
	if anyContains(anonGot, `"table":"priv"`) {
		t.Fatalf("anon leaked private event: %v", anonGot)
	}
}

func waitForSubscribers(t *testing.T, bus *events.Hub, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if bus.SubscriberCount() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscribers did not reach %d (have %d)", n, bus.SubscriberCount())
}

func drain(ch <-chan string, d time.Duration) []string {
	var out []string
	deadline := time.After(d)
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, s)
		case <-deadline:
			return out
		}
	}
}

func readSSEFor(body io.Reader, d time.Duration) string {
	type res struct{ s string }
	ch := make(chan res, 1)
	go func() {
		sc := bufio.NewScanner(body)
		var b strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				b.WriteString(data)
				b.WriteString("\n")
				ch <- res{b.String()}
				return
			}
		}
		ch <- res{b.String()}
	}()
	select {
	case r := <-ch:
		return r.s
	case <-time.After(d):
		return ""
	}
}

func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
