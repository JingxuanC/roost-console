package billing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrOAuthInvalid = errors.New("invalid oauth request")

func randomURLToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Store) CreateOAuthCode(ctx context.Context, clientID, userID, email, orgID, redirectURI, challenge string, ttl time.Duration) (string, error) {
	code := randomURLToken()
	_, err := s.Pool.Exec(ctx, `
		insert into billing.oauth_codes (code_hash,client_id,user_id,email,org_id,redirect_uri,code_challenge,expires_at)
		values ($1,$2,$3,$4,$5,$6,$7,now()+make_interval(secs=>$8))`,
		tokenHash(code), clientID, userID, email, orgID, redirectURI, challenge, int(ttl.Seconds()))
	return code, err
}

type OAuthGrant struct {
	UserID string
	Email  string
	OrgID  string
}

func (s *Store) ConsumeOAuthCode(ctx context.Context, code, clientID, redirectURI, verifier string) (OAuthGrant, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return OAuthGrant{}, err
	}
	defer tx.Rollback(ctx)
	var g OAuthGrant
	var challenge string
	var used *time.Time
	var expires time.Time
	err = tx.QueryRow(ctx, `
		select user_id,email,org_id,code_challenge,used_at,expires_at
		from billing.oauth_codes where code_hash=$1 for update`, tokenHash(code)).
		Scan(&g.UserID, &g.Email, &g.OrgID, &challenge, &used, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthGrant{}, ErrOAuthInvalid
	}
	if err != nil {
		return OAuthGrant{}, err
	}
	if used != nil || time.Now().After(expires) || clientID == "" || redirectURI == "" {
		return OAuthGrant{}, ErrOAuthInvalid
	}
	if verifier == "" || PKCEChallenge(verifier) != challenge {
		return OAuthGrant{}, ErrOAuthInvalid
	}
	var storedClient, storedRedirect string
	if err := tx.QueryRow(ctx, `select client_id,redirect_uri from billing.oauth_codes where code_hash=$1`, tokenHash(code)).Scan(&storedClient, &storedRedirect); err != nil {
		return OAuthGrant{}, err
	}
	if storedClient != clientID || storedRedirect != redirectURI {
		return OAuthGrant{}, ErrOAuthInvalid
	}
	if _, err := tx.Exec(ctx, `update billing.oauth_codes set used_at=now() where code_hash=$1`, tokenHash(code)); err != nil {
		return OAuthGrant{}, err
	}
	return g, tx.Commit(ctx)
}

func (s *Store) CreateOAuthToken(ctx context.Context, clientID, userID, email, orgID string, ttl time.Duration) (string, time.Time, error) {
	token := randomURLToken()
	expires := time.Now().Add(ttl)
	_, err := s.Pool.Exec(ctx, `
		insert into billing.oauth_tokens (token_hash,client_id,user_id,email,org_id,expires_at)
		values ($1,$2,$3,$4,$5,$6)`,
		tokenHash(token), clientID, userID, email, orgID, expires)
	return token, expires, err
}

func (s *Store) OAuthTokenUser(ctx context.Context, token string) (OAuthGrant, time.Time, error) {
	var g OAuthGrant
	var expires time.Time
	err := s.Pool.QueryRow(ctx, `
		select user_id,email,org_id,expires_at from billing.oauth_tokens where token_hash=$1`, tokenHash(token)).
		Scan(&g.UserID, &g.Email, &g.OrgID, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthGrant{}, time.Time{}, ErrOAuthInvalid
	}
	if err != nil {
		return OAuthGrant{}, time.Time{}, err
	}
	if time.Now().After(expires) {
		return OAuthGrant{}, time.Time{}, ErrOAuthInvalid
	}
	return g, expires, nil
}
