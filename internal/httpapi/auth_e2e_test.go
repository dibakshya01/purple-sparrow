package httpapi

import (
	"net/http"
	"testing"
)

// TestE2E_UserTokenFlowsIntoPolicy proves a signed user token resolves to an
// authenticated principal whose auth.uid() scopes data access via the policy
// engine — the M1+M2 integration.
func TestE2E_UserTokenFlowsIntoPolicy(t *testing.T) {
	s := dataServer(t)

	// Admin sets up an owner-scoped table with insert+select policies.
	if rec := do(t, s, "POST", "/v1/tables", testAdminKey,
		`{"name":"posts","columns":[{"name":"owner_id","type":"text"},{"name":"body","type":"text"}]}`); rec.Code != http.StatusCreated {
		t.Fatalf("create table: %d %s", rec.Code, rec.Body.String())
	}
	for _, body := range []string{
		`{"table":"posts","action":"insert","roles":["authenticated"],"check":"auth.uid() = owner_id"}`,
		`{"table":"posts","action":"select","roles":["authenticated"],"using":"auth.uid() = owner_id"}`,
	} {
		if rec := do(t, s, "POST", "/v1/policies", testAdminKey, body); rec.Code != http.StatusCreated {
			t.Fatalf("create policy: %d %s", rec.Code, rec.Body.String())
		}
	}

	// Two users sign up.
	alice := signup(t, s, "alice@example.com", "password1")
	bob := signup(t, s, "bob@example.com", "password1")

	// /me reflects the token's subject.
	meRec := do(t, s, "GET", "/v1/auth/me", alice.token, "")
	if meRec.Code != http.StatusOK || decode(t, meRec)["subject"] != alice.id {
		t.Fatalf("/me subject mismatch: %s", meRec.Body.String())
	}

	// Alice inserts a post owned by herself -> allowed by the insert check.
	ins := do(t, s, "POST", "/v1/tables/posts/records", alice.token,
		`{"owner_id":"`+alice.id+`","body":"hello from alice"}`)
	if ins.Code != http.StatusCreated {
		t.Fatalf("alice insert: %d %s", ins.Code, ins.Body.String())
	}

	// Alice trying to insert a post owned by Bob -> blocked by the check.
	spoof := do(t, s, "POST", "/v1/tables/posts/records", alice.token,
		`{"owner_id":"`+bob.id+`","body":"spoof"}`)
	if spoof.Code != http.StatusForbidden {
		t.Fatalf("alice spoofing owner: want 403, got %d %s", spoof.Code, spoof.Body.String())
	}

	// Alice sees her row; Bob sees none.
	if n := len(decode(t, do(t, s, "GET", "/v1/tables/posts/records", alice.token, ""))["records"].([]any)); n != 1 {
		t.Fatalf("alice should see 1 row, saw %d", n)
	}
	if n := len(decode(t, do(t, s, "GET", "/v1/tables/posts/records", bob.token, ""))["records"].([]any)); n != 0 {
		t.Fatalf("bob should see 0 rows, saw %d", n)
	}
}

type userTok struct{ id, token string }

func signup(t *testing.T, s *Server, email, password string) userTok {
	t.Helper()
	rec := do(t, s, "POST", "/v1/auth/signup", "", `{"email":"`+email+`","password":"`+password+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup %s: %d %s", email, rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	token, _ := m["access_token"].(string)
	user, _ := m["user"].(map[string]any)
	id, _ := user["id"].(string)
	if token == "" || id == "" {
		t.Fatalf("signup response missing token/id: %s", rec.Body.String())
	}
	return userTok{id: id, token: token}
}
