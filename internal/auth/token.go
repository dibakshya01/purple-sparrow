package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer    = "orange-crow"
	accessTTL = 15 * time.Minute
	clockSkew = 30 * time.Second
)

// Claims is the access-token payload.
type Claims struct {
	Roles []string `json:"roles"`
	jwt.RegisteredClaims
}

// issueAccess signs an RS256 access token for the subject and roles.
func (ks *keyStore) issueAccess(subject string, roles []string) (string, error) {
	now := time.Now()
	claims := Claims{
		Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   subject,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTTL)),
			NotBefore: jwt.NewNumericDate(now.Add(-clockSkew)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = ks.active.kid
	return tok.SignedString(ks.active.priv)
}

// verifyAccess parses and validates a token. It enforces RS256 ONLY (blocking the
// alg=none and RS256->HS256 confusion attacks), a known kid, the issuer, and
// expiry with a small skew.
func (ks *keyStore) verifyAccess(tokenStr string) (*Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(issuer),
		jwt.WithLeeway(clockSkew),
		jwt.WithExpirationRequired(),
	)
	claims := &Claims{}
	_, err := parser.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		// Defense in depth: reject anything that is not RSA even before WithValidMethods.
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		pub, ok := ks.publicKey(kid)
		if !ok {
			return nil, fmt.Errorf("unknown key id")
		}
		return pub, nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}
