package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dibakshya01/orange-crow/internal/apierr"
	"github.com/dibakshya01/orange-crow/internal/auth"
	"github.com/dibakshya01/orange-crow/internal/principal"
)

// principalMiddleware resolves the caller's principal via the auth service:
// a JWT or API key -> its principal; a presented-but-invalid credential -> 401
// (fail closed, never a silent downgrade); no credential -> anon.
func principalMiddleware(a *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented := bearerOrHeader(r)
			p, err := a.Resolve(r.Context(), presented)
			if err != nil {
				if errors.Is(err, auth.ErrInvalidCredentials) {
					apierr.Write(w, r, invalidCredentials())
					return
				}
				apierr.Write(w, r, apierr.Internal("").WithInternal(err))
				return
			}
			ctx := principal.WithContext(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerOrHeader(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(h)
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

func invalidCredentials() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, "invalid_credentials",
		"The presented credential was not recognized.",
		"Provide a valid access token or API key as `Authorization: Bearer <value>` (or `X-API-Key`), or omit it to act as anon.",
		"/docs/errors#invalid_credentials")
}
