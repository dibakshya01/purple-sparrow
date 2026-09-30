package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

// principalMiddleware resolves the caller's principal (M1 bridge; ADR notes M2
// replaces it with full JWT/key auth). Rules:
//   - a presented admin key that matches -> project_admin;
//   - a presented credential that does NOT match -> 401 (fail closed, explicit,
//     never a silent downgrade that would mask credential probing);
//   - no credential -> anon.
func principalMiddleware(adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented := bearerOrHeader(r)
			var p principal.Principal
			switch {
			case presented == "":
				p = principal.Anon()
			case adminKey != "" && constantEq(presented, adminKey):
				p = principal.Principal{Roles: []string{principal.RoleProjectAdmin}}
			default:
				apierr.Write(w, r, invalidCredentials())
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

func constantEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func invalidCredentials() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, "invalid_credentials",
		"The presented credential was not recognized.",
		"Provide a valid admin API key as `Authorization: Bearer <key>` or `X-API-Key`, or omit it to act as anon.",
		"/docs/errors#invalid_credentials")
}
