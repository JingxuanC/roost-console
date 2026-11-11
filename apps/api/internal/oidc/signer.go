package oidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"time"
)

type Signer struct {
	key *rsa.PrivateKey
	kid string
}

func NewSigner() (*Signer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Signer{key: key, kid: "roost-1"}, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (s *Signer) SignIDToken(issuer, clientID, userID, email, orgID, nonce string, ttl time.Duration) (string, error) {
	now := time.Now()
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": s.kid}
	claims := map[string]any{
		"iss": issuer, "sub": userID, "aud": clientID,
		"iat": now.Unix(), "exp": now.Add(ttl).Unix(),
		"email": email, "email_verified": true, "org_id": orgID,
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signingInput := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + b64(sig), nil
}

func (s *Signer) JWKS() map[string]any {
	e := big.NewInt(int64(s.key.PublicKey.E)).Bytes()
	return map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": s.kid,
		"n": b64(s.key.PublicKey.N.Bytes()), "e": b64(e),
	}}}
}
