package httpapi

import (
	"net/http"
	"testing"
)

func TestE2E_Docs(t *testing.T) {
	s := dataServer(t)

	idx := do(t, s, "GET", "/docs", "", "")
	if idx.Code != http.StatusOK {
		t.Fatalf("GET /docs: %d", idx.Code)
	}
	if len(decode(t, idx)["docs"].([]any)) == 0 {
		t.Fatal("expected at least one doc slug")
	}
	if rec := do(t, s, "GET", "/docs/errors", "", ""); rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("GET /docs/errors: %d len=%d", rec.Code, rec.Body.Len())
	}
	if rec := do(t, s, "GET", "/docs/nope", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown doc should 404, got %d", rec.Code)
	}
}

func TestE2E_MemoryIsolationAndAnonRefused(t *testing.T) {
	s := dataServer(t)
	alice := signup(t, s, "mem-alice@example.com", "password1")
	bob := signup(t, s, "mem-bob@example.com", "password1")

	// Anon cannot write memory.
	if rec := do(t, s, "PUT", "/v1/memory/prefs/theme", "", `"dark"`); rec.Code != http.StatusForbidden {
		t.Fatalf("anon memory write should be 403, got %d", rec.Code)
	}

	// Alice stores and reads back.
	if rec := do(t, s, "PUT", "/v1/memory/prefs/theme", alice.token, `"dark"`); rec.Code != http.StatusOK {
		t.Fatalf("alice set: %d %s", rec.Code, rec.Body.String())
	}
	got := do(t, s, "GET", "/v1/memory/prefs/theme", alice.token, "")
	if got.Code != http.StatusOK || decode(t, got)["value"] != "dark" {
		t.Fatalf("alice get: %d %s", got.Code, got.Body.String())
	}

	// Bob cannot see Alice's key (subject isolation).
	if rec := do(t, s, "GET", "/v1/memory/prefs/theme", bob.token, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob must not read alice's memory, got %d", rec.Code)
	}
}

func TestE2E_AdvisorFlagsPolicylessTable(t *testing.T) {
	s := dataServer(t)
	if rec := do(t, s, "POST", "/v1/tables", testAdminKey,
		`{"name":"secrets","columns":[{"name":"v","type":"text"}]}`); rec.Code != http.StatusCreated {
		t.Fatalf("create table: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(t, s, "GET", "/advisor", testAdminKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("advisor: %d %s", rec.Code, rec.Body.String())
	}
	findings := decode(t, rec)["findings"].([]any)
	found := false
	for _, f := range findings {
		if f.(map[string]any)["code"] == "table_no_policies" {
			found = true
		}
	}
	if !found {
		t.Fatalf("advisor should flag table_no_policies, got %s", rec.Body.String())
	}
	// Advisor is admin-only.
	if rec := do(t, s, "GET", "/advisor", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anon advisor should be 403, got %d", rec.Code)
	}
}
