package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dibakshya01/orange-crow/internal/data"
	"github.com/dibakshya01/orange-crow/internal/idgen"
	"github.com/dibakshya01/orange-crow/internal/principal"
)

const (
	refreshTTL   = 30 * 24 * time.Hour
	minPassword  = 8
	apiKeyPrefix = "oc_sk_"
)

// Sentinel errors (surfaced generically by the HTTP layer to avoid enumeration).
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email already registered")
	ErrWeakPassword       = errors.New("password too short")
	ErrInvalidRefresh     = errors.New("invalid or expired refresh token")
	ErrInvalidEmail       = errors.New("invalid email")
)

// User is a public view of an account (never includes the password hash).
type User struct {
	ID    string   `json:"id"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

// Tokens is an access + refresh pair.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// Service implements identity operations.
type Service struct {
	eng  data.Engine
	keys *keyStore
}

// NewService builds the auth Service, ensuring an active signing keypair exists.
func NewService(ctx context.Context, eng data.Engine) (*Service, error) {
	ks, err := newKeyStore(ctx, eng)
	if err != nil {
		return nil, err
	}
	return &Service{eng: eng, keys: ks}, nil
}

// JWKS returns the JSON Web Key Set.
func (s *Service) JWKS() map[string]any { return s.keys.JWKS() }

// Signup creates an account with the authenticated role (roles are assigned
// server-side; a client cannot self-grant privileges).
func (s *Service) Signup(ctx context.Context, email, password string) (*User, *Tokens, error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return nil, nil, ErrInvalidEmail
	}
	if len(password) < minPassword {
		return nil, nil, ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, nil, err
	}
	roles := []string{principal.RoleAuthenticated}
	rolesJSON, _ := json.Marshal(roles)
	u := &User{ID: idgen.NewUUID(), Email: email, Roles: roles}

	_, err = s.eng.ExecCtx(ctx,
		`INSERT INTO _oc_users (id, email, password_hash, roles, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, email, hash, string(rolesJSON), idgen.NowRFC3339())
	if err != nil {
		if isUniqueViolation(err) {
			return nil, nil, ErrEmailTaken
		}
		return nil, nil, err
	}
	toks, err := s.issueTokens(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	return u, toks, nil
}

// Login verifies credentials and issues tokens. A wrong password and an unknown
// email return the same generic error (no user enumeration).
func (s *Service) Login(ctx context.Context, email, password string) (*User, *Tokens, error) {
	email = normalizeEmail(email)
	row, err := s.eng.QueryRowCtx(ctx,
		`SELECT id, email, password_hash, roles FROM _oc_users WHERE email = ?`, email)
	if errors.Is(err, data.ErrNoRows) {
		// Run a bcrypt comparison against a dummy hash to reduce timing signal.
		_ = VerifyPassword("$2a$12$C6UzMDM.H6dfI/f/IKcEeO4Y5r0Q0Z8b8p6f0e6a6b6c6d6e6f6g6", password)
		return nil, nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, nil, err
	}
	if !VerifyPassword(str(row["password_hash"]), password) {
		return nil, nil, ErrInvalidCredentials
	}
	u := &User{ID: str(row["id"]), Email: str(row["email"]), Roles: decodeRoles(str(row["roles"]))}
	toks, err := s.issueTokens(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	return u, toks, nil
}

// Refresh validates a refresh token, marks it used (single-use rotation), and
// issues a fresh token pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	h := sha256Hex(refreshToken)
	var out *Tokens
	err := s.eng.Transact(ctx, func(q data.Querier) error {
		row, err := q.QueryRowCtx(ctx,
			`SELECT id, user_id, expires_at, used FROM _oc_refresh_tokens WHERE token_hash = ?`, h)
		if errors.Is(err, data.ErrNoRows) {
			return ErrInvalidRefresh
		}
		if err != nil {
			return err
		}
		if toInt(row["used"]) == 1 {
			return ErrInvalidRefresh
		}
		exp, perr := time.Parse(time.RFC3339Nano, str(row["expires_at"]))
		if perr != nil || time.Now().After(exp) {
			return ErrInvalidRefresh
		}
		// Conditional consume closes the check-then-act race: on a multi-connection
		// engine two concurrent refreshes both read used=0, but only one UPDATE
		// where used=0 affects a row; the loser gets 0 rows and is rejected.
		n, err := q.ExecCtx(ctx, `UPDATE _oc_refresh_tokens SET used = 1 WHERE id = ? AND used = 0`, str(row["id"]))
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrInvalidRefresh
		}
		urow, err := q.QueryRowCtx(ctx, `SELECT id, email, roles FROM _oc_users WHERE id = ?`, str(row["user_id"]))
		if err != nil {
			return err
		}
		u := &User{ID: str(urow["id"]), Email: str(urow["email"]), Roles: decodeRoles(str(urow["roles"]))}
		access, err := s.keys.issueAccess(u.ID, u.Roles)
		if err != nil {
			return err
		}
		refresh, err := s.mintRefresh(ctx, q, u.ID)
		if err != nil {
			return err
		}
		out = &Tokens{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: int(accessTTL.Seconds())}
		return nil
	})
	return out, err
}

// Resolve turns a presented credential into a principal. Empty -> anon. A oc_
// prefixed value is an API key (hash lookup); anything else is a JWT. An invalid
// credential is an error (401), never a silent downgrade.
func (s *Service) Resolve(ctx context.Context, presented string) (principal.Principal, error) {
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return principal.Anon(), nil
	}
	if strings.HasPrefix(presented, "oc_") {
		row, err := s.eng.QueryRowCtx(ctx, `SELECT roles FROM _oc_api_keys WHERE key_hash = ?`, sha256Hex(presented))
		if errors.Is(err, data.ErrNoRows) {
			return principal.Principal{}, ErrInvalidCredentials
		}
		if err != nil {
			return principal.Principal{}, err
		}
		return principal.Principal{Roles: decodeRoles(str(row["roles"]))}, nil
	}
	claims, err := s.keys.verifyAccess(presented)
	if err != nil {
		return principal.Principal{}, ErrInvalidCredentials
	}
	return principal.Principal{Roles: claims.Roles, Subject: claims.Subject}, nil
}

// CreateAPIKey generates a key, stores only its hash, and returns the plaintext
// ONCE. roles are validated by the caller/handler.
func (s *Service) CreateAPIKey(ctx context.Context, name string, roles []string) (id, plaintext string, err error) {
	plaintext = apiKeyPrefix + randHex(24)
	id = idgen.NewUUID()
	rolesJSON, _ := json.Marshal(roles)
	_, err = s.eng.ExecCtx(ctx,
		`INSERT INTO _oc_api_keys (id, name, key_hash, roles, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, name, sha256Hex(plaintext), string(rolesJSON), idgen.NowRFC3339())
	if err != nil {
		return "", "", err
	}
	return id, plaintext, nil
}

// ListAPIKeys returns key metadata (never the secret).
func (s *Service) ListAPIKeys(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.eng.QueryCtx(ctx, `SELECT id, name, roles, created_at FROM _oc_api_keys ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id": str(r["id"]), "name": str(r["name"]),
			"roles": decodeRoles(str(r["roles"])), "created_at": str(r["created_at"]),
		})
	}
	return out, nil
}

// SeedAdminKey registers a fixed admin key (from OC_ADMIN_API_KEY) as a
// project_admin key if it is not already present. Idempotent.
func (s *Service) SeedAdminKey(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	h := sha256Hex(key)
	_, err := s.eng.QueryRowCtx(ctx, `SELECT id FROM _oc_api_keys WHERE key_hash = ?`, h)
	if err == nil {
		return nil // already present
	}
	if !errors.Is(err, data.ErrNoRows) {
		return err
	}
	rolesJSON, _ := json.Marshal([]string{principal.RoleProjectAdmin})
	_, err = s.eng.ExecCtx(ctx,
		`INSERT INTO _oc_api_keys (id, name, key_hash, roles, created_at) VALUES (?, ?, ?, ?, ?)`,
		idgen.NewUUID(), "seed-admin", h, string(rolesJSON), idgen.NowRFC3339())
	return err
}

func (s *Service) issueTokens(ctx context.Context, u *User) (*Tokens, error) {
	access, err := s.keys.issueAccess(u.ID, u.Roles)
	if err != nil {
		return nil, err
	}
	var refresh string
	err = s.eng.Transact(ctx, func(q data.Querier) error {
		refresh, err = s.mintRefresh(ctx, q, u.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: int(accessTTL.Seconds())}, nil
}

func (s *Service) mintRefresh(ctx context.Context, q data.Querier, userID string) (string, error) {
	token := randHex(32)
	_, err := q.ExecCtx(ctx,
		`INSERT INTO _oc_refresh_tokens (id, user_id, token_hash, expires_at, used, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		idgen.NewUUID(), userID, sha256Hex(token), time.Now().Add(refreshTTL).UTC().Format(time.RFC3339Nano), idgen.NowRFC3339())
	if err != nil {
		return "", err
	}
	return token, nil
}

// --- helpers --------------------------------------------------------------

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func validEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	return at > 0 && at < len(e)-1 && !strings.ContainsAny(e, " \t\n")
}

func decodeRoles(s string) []string {
	var roles []string
	if json.Unmarshal([]byte(s), &roles) != nil || len(roles) == 0 {
		return []string{principal.RoleAnon}
	}
	return roles
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isUniqueViolation(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}
