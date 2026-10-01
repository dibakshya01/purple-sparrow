package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/dibakshya01/orange-crow/internal/data"
	"github.com/dibakshya01/orange-crow/internal/data/migrate"
	"github.com/dibakshya01/orange-crow/internal/principal"
)

func newSvc(t *testing.T) (*Service, data.Engine) {
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
	svc, err := NewService(ctx, eng)
	if err != nil {
		t.Fatal(err)
	}
	return svc, eng
}

func TestSignupLoginResolve(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)

	u, toks, err := s.Signup(ctx, "  Alice@Example.com ", "hunter2pass")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email should be normalized, got %q", u.Email)
	}
	if len(u.Roles) != 1 || u.Roles[0] != principal.RoleAuthenticated {
		t.Errorf("signup must assign authenticated only, got %v", u.Roles)
	}

	p, err := s.Resolve(ctx, toks.AccessToken)
	if err != nil {
		t.Fatalf("resolve access token: %v", err)
	}
	if p.Subject != u.ID || !p.HasRole(principal.RoleAuthenticated) {
		t.Fatalf("resolved principal mismatch: %+v", p)
	}

	if _, _, err := s.Login(ctx, "alice@example.com", "hunter2pass"); err != nil {
		t.Fatalf("login: %v", err)
	}
}

func TestSignupCannotSelfAssignAdmin(t *testing.T) {
	// The API never reads a client role at signup; verify the stored role is only
	// authenticated even though we only pass email+password.
	ctx := context.Background()
	s, _ := newSvc(t)
	u, _, err := s.Signup(ctx, "b@example.com", "password1")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range u.Roles {
		if role == principal.RoleProjectAdmin {
			t.Fatal("signup must never yield project_admin")
		}
	}
}

func TestGenericErrorsNoEnumeration(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	if _, _, err := s.Signup(ctx, "c@example.com", "password1"); err != nil {
		t.Fatal(err)
	}
	_, _, wrongPass := s.Login(ctx, "c@example.com", "wrong-password")
	_, _, unknown := s.Login(ctx, "nobody@example.com", "whatever12")
	if !errors.Is(wrongPass, ErrInvalidCredentials) || !errors.Is(unknown, ErrInvalidCredentials) {
		t.Fatalf("both must be ErrInvalidCredentials, got %v / %v", wrongPass, unknown)
	}
}

func TestRefreshIsSingleUse(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	_, toks, err := s.Signup(ctx, "d@example.com", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(ctx, toks.RefreshToken); err != nil {
		t.Fatalf("first refresh should work: %v", err)
	}
	if _, err := s.Refresh(ctx, toks.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("reusing a refresh token must fail, got %v", err)
	}
}

func TestAlgConfusionAndNoneRejected(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	_, _, err := s.Signup(ctx, "e@example.com", "password1")
	if err != nil {
		t.Fatal(err)
	}
	kid := s.keys.active.kid
	claims := Claims{
		Roles: []string{principal.RoleProjectAdmin},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Subject: "attacker",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	// HS256 forgery (algorithm-confusion): signed with an attacker secret.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hs.Header["kid"] = kid
	hsSigned, _ := hs.SignedString([]byte("attacker-secret"))
	if _, err := s.keys.verifyAccess(hsSigned); err == nil {
		t.Fatal("HS256 token must be rejected (algorithm confusion)")
	}

	// alg=none forgery.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	none.Header["kid"] = kid
	noneSigned, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := s.keys.verifyAccess(noneSigned); err == nil {
		t.Fatal("alg=none token must be rejected")
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	if _, _, err := s.Signup(ctx, "f@example.com", "password1"); err != nil {
		t.Fatal(err)
	}
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Subject: "f",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-30 * time.Minute)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.keys.active.kid
	signed, _ := tok.SignedString(s.keys.active.priv)
	if _, err := s.keys.verifyAccess(signed); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestResolveInvalidAndAnon(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)

	if p, err := s.Resolve(ctx, ""); err != nil || !p.HasRole(principal.RoleAnon) {
		t.Fatalf("empty credential must be anon, got %+v %v", p, err)
	}
	if _, err := s.Resolve(ctx, "oc_sk_does-not-exist"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown api key must be invalid, got %v", err)
	}
	if _, err := s.Resolve(ctx, "not-a-real-jwt"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("garbage token must be invalid, got %v", err)
	}
}

func TestAPIKeyStoredHashedAndResolvable(t *testing.T) {
	ctx := context.Background()
	s, eng := newSvc(t)
	_, plaintext, err := s.CreateAPIKey(ctx, "ci", []string{principal.RoleProjectAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plaintext, "oc_sk_") {
		t.Fatalf("api key should have oc_sk_ prefix, got %q", plaintext)
	}
	p, err := s.Resolve(ctx, plaintext)
	if err != nil || !p.IsAdmin() {
		t.Fatalf("api key should resolve to admin, got %+v %v", p, err)
	}
	// The plaintext must NOT be stored anywhere; only its hash.
	rows, err := eng.QueryCtx(ctx, `SELECT key_hash FROM _oc_api_keys`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r["key_hash"] == plaintext {
			t.Fatal("plaintext api key must never be stored")
		}
	}
	// A wrong key is rejected.
	if _, err := s.Resolve(ctx, "oc_sk_wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong key must be rejected, got %v", err)
	}
}
