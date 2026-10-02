package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"

	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
)

// signingKey is an active RSA keypair used to sign/verify access tokens.
type signingKey struct {
	kid  string
	priv *rsa.PrivateKey
	pub  *rsa.PublicKey
}

// keyStore persists and loads RSA keypairs. JWKS publishes the public keys so
// tokens can be verified externally. Multiple keys are supported so the signing
// key can be rotated (new active key signs; old public keys still verify).
type keyStore struct {
	eng    data.Engine
	active *signingKey
	all    map[string]*signingKey // kid -> key (for verification)
}

func newKeyStore(ctx context.Context, eng data.Engine) (*keyStore, error) {
	ks := &keyStore{eng: eng, all: map[string]*signingKey{}}
	if err := ks.load(ctx); err != nil {
		return nil, err
	}
	if ks.active == nil {
		if err := ks.generateActive(ctx); err != nil {
			return nil, err
		}
	}
	return ks, nil
}

func (ks *keyStore) load(ctx context.Context) error {
	rows, err := ks.eng.QueryCtx(ctx, `SELECT kid, private_pem, public_pem, active FROM _ps_keypairs`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		kid, _ := r["kid"].(string)
		priv, err := parsePrivatePEM(str(r["private_pem"]))
		if err != nil {
			return fmt.Errorf("keypair %s: %w", kid, err)
		}
		sk := &signingKey{kid: kid, priv: priv, pub: &priv.PublicKey}
		ks.all[kid] = sk
		if toInt(r["active"]) == 1 {
			ks.active = sk
		}
	}
	return nil
}

func (ks *keyStore) generateActive(ctx context.Context) error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	kid := idgen.NewUUID()
	privPEM := encodePrivatePEM(priv)
	pubPEM, err := encodePublicPEM(&priv.PublicKey)
	if err != nil {
		return err
	}
	_, err = ks.eng.ExecCtx(ctx,
		`INSERT INTO _ps_keypairs (kid, private_pem, public_pem, active, created_at) VALUES (?, ?, ?, 1, ?)`,
		kid, privPEM, pubPEM, idgen.NowRFC3339())
	if err != nil {
		return err
	}
	sk := &signingKey{kid: kid, priv: priv, pub: &priv.PublicKey}
	ks.all[kid] = sk
	ks.active = sk
	return nil
}

func (ks *keyStore) publicKey(kid string) (*rsa.PublicKey, bool) {
	if sk, ok := ks.all[kid]; ok {
		return sk.pub, true
	}
	return nil, false
}

// JWKS returns the JSON Web Key Set for all known public keys.
func (ks *keyStore) JWKS() map[string]any {
	var keys []map[string]any
	for _, sk := range ks.all {
		keys = append(keys, map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": sk.kid,
			"n":   base64.RawURLEncoding.EncodeToString(sk.pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(sk.pub.E)).Bytes()),
		})
	}
	return map[string]any{"keys": keys}
}

func encodePrivatePEM(priv *rsa.PrivateKey) string {
	der := x509.MarshalPKCS1PrivateKey(priv)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

func encodePublicPEM(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

func parsePrivatePEM(s string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}
