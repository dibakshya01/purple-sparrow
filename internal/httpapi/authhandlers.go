package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/auth"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

func (s *Server) mountAuthRoutes(g chi.Router) {
	g.Post("/v1/auth/signup", s.handleSignup)
	g.Post("/v1/auth/login", s.handleLogin)
	g.Post("/v1/auth/refresh", s.handleRefresh)
	g.Get("/v1/auth/me", s.handleMe)
	g.Post("/v1/auth/api-keys", s.handleCreateAPIKey)
	g.Get("/v1/auth/api-keys", s.handleListAPIKeys)
}

type credsBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var in credsBody
	if !decodeJSON(w, r, &in) {
		return
	}
	u, toks, err := s.deps.Auth.Signup(r.Context(), in.Email, in.Password)
	if err != nil {
		apierr.Write(w, r, mapAuthError(err))
		return
	}
	writeJSON(w, r, http.StatusCreated, tokenResponse(u, toks))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credsBody
	if !decodeJSON(w, r, &in) {
		return
	}
	u, toks, err := s.deps.Auth.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		apierr.Write(w, r, mapAuthError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, tokenResponse(u, toks))
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	toks, err := s.deps.Auth.Refresh(r.Context(), in.RefreshToken)
	if err != nil {
		apierr.Write(w, r, mapAuthError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, toks)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]any{
		"subject": p.Subject,
		"roles":   p.Roles,
		"is_admin": p.IsAdmin(),
	})
}

func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, s.deps.Auth.JWKS())
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Name == "" {
		apierr.Write(w, r, apierr.BadRequest("An API key name is required.", "Provide a non-empty `name`."))
		return
	}
	if len(in.Roles) == 0 {
		in.Roles = []string{principal.RoleProjectAdmin}
	}
	for _, role := range in.Roles {
		if !validRole(role) {
			apierr.Write(w, r, apierr.BadRequest("Invalid role: "+role,
				"Roles must be a subset of anon, authenticated, project_admin."))
			return
		}
	}
	id, plaintext, err := s.deps.Auth.CreateAPIKey(r.Context(), in.Name, in.Roles)
	if err != nil {
		apierr.Write(w, r, apierr.Internal("").WithInternal(err))
		return
	}
	// The secret is shown exactly once.
	writeJSON(w, r, http.StatusCreated, map[string]any{
		"id": id, "name": in.Name, "roles": in.Roles, "key": plaintext,
		"note": "Store this key now; it is shown only once and cannot be retrieved later.",
	})
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	keys, err := s.deps.Auth.ListAPIKeys(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.Internal("").WithInternal(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"api_keys": keys})
}

func tokenResponse(u *auth.User, t *auth.Tokens) map[string]any {
	return map[string]any{
		"access_token":  t.AccessToken,
		"refresh_token": t.RefreshToken,
		"token_type":    t.TokenType,
		"expires_in":    t.ExpiresIn,
		"user":          u,
	}
}

func validRole(role string) bool {
	switch role {
	case principal.RoleAnon, principal.RoleAuthenticated, principal.RoleProjectAdmin:
		return true
	}
	return false
}

func mapAuthError(err error) *apierr.Error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return apierr.New(http.StatusUnauthorized, "invalid_credentials",
			"Invalid email or password.",
			"Check the credentials. For security, the same error is returned for an unknown email and a wrong password.",
			"/docs/auth#login")
	case errors.Is(err, auth.ErrEmailTaken):
		return apierr.New(http.StatusConflict, "email_taken",
			"An account with this email already exists.",
			"Log in instead, or use a different email.", "/docs/auth#signup")
	case errors.Is(err, auth.ErrWeakPassword):
		return apierr.New(http.StatusBadRequest, "weak_password",
			"Password is too short.", "Use a password of at least 8 characters.", "/docs/auth#signup")
	case errors.Is(err, auth.ErrInvalidEmail):
		return apierr.New(http.StatusBadRequest, "invalid_email",
			"The email address is not valid.", "Provide a valid email address.", "/docs/auth#signup")
	case errors.Is(err, auth.ErrInvalidRefresh):
		return apierr.New(http.StatusUnauthorized, "invalid_refresh",
			"The refresh token is invalid, expired, or already used.",
			"Log in again to obtain a new token pair. Refresh tokens are single-use.", "/docs/auth#refresh")
	default:
		return apierr.Internal("").WithInternal(err)
	}
}
