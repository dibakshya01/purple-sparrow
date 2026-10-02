# Auth

## Credentials
Send one of these as `Authorization: Bearer <value>` (or `X-API-Key`):
- **User access token** — a short-lived RS256 JWT from signup/login. Verify
  externally via JWKS at `/.well-known/jwks.json`.
- **API key** `ps_sk_...` — server-to-server; carries roles (e.g. project_admin).
- **(none)** — you are `anon`.

An invalid credential returns `401`; omitting it makes you `anon`.

## Endpoints
- `POST /v1/auth/signup` `{email, password}` → `{access_token, refresh_token, user}`.
  New users get the `authenticated` role (roles are assigned server-side).
- `POST /v1/auth/login` `{email, password}` → tokens. Wrong email and wrong
  password return the same generic error.
- `POST /v1/auth/refresh` `{refresh_token}` → new tokens. Refresh tokens are
  single-use (rotated on each refresh).
- `GET  /v1/auth/me` → the current principal.
- `POST /v1/auth/api-keys` `{name, roles}` (admin) → the key, shown once.

## Roles
`anon` < `authenticated` < `project_admin`. Policies grant actions to roles;
`project_admin` bypasses policies. In policy expressions, `auth.uid()` is the
user id and `auth.role()` the role.
