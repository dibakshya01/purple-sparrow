// Package auth implements identity: password auth, RS256 tokens + JWKS, and a
// hashed API-key store. It produces a principal.Principal consumed by the M1
// policy engine.
package auth

import (
	"crypto/sha256"
	"encoding/base64"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is deliberately >= 12.
const bcryptCost = 12

// prehash maps an arbitrary-length password to a fixed 44-char base64 string
// before bcrypt. This sidesteps bcrypt's 72-byte truncation (a long password
// wouldn't be fully considered) and its NUL-byte truncation (raw SHA-256 can
// contain 0x00), without weakening entropy.
func prehash(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	return []byte(base64.StdEncoding.EncodeToString(sum[:]))
}

// HashPassword returns a bcrypt hash of the password.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword(prehash(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword reports whether password matches the stored bcrypt hash. It runs
// bcrypt even conceptually on mismatch paths at the call site to avoid trivial
// timing oracles (callers use a generic error regardless).
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), prehash(password)) == nil
}
