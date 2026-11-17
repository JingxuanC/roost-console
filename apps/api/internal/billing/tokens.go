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

type ProductToken struct {
	TokenHash  string     `json:"token_hash"`
	OrgID      string     `json:"org_id"`
	Product    string     `json:"product"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func hashProductToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) IssueProductToken(ctx context.Context, orgID, product string) (string, ProductToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ProductToken{}, err
	}
	prefix := "cm_"
	if product == "relay" {
		prefix = "rk_"
	}
	token := prefix + base64.RawURLEncoding.EncodeToString(raw)
	var t ProductToken
	err := s.Pool.QueryRow(ctx, `
		insert into billing.product_tokens (token_hash,org_id,product)
		values ($1,$2,$3) returning token_hash,org_id,product,created_at`,
		hashProductToken(token), orgID, product).Scan(&t.TokenHash, &t.OrgID, &t.Product, &t.CreatedAt)
	return token, t, err
}

func (s *Store) ListProductTokens(ctx context.Context, orgID, product string) ([]ProductToken, error) {
	rows, err := s.Pool.Query(ctx, `
		select token_hash,org_id,product,created_at,last_used_at,revoked_at
		from billing.product_tokens
		where org_id=$1 and product=$2 and revoked_at is null
		order by created_at desc`, orgID, product)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProductToken, 0)
	for rows.Next() {
		var t ProductToken
		if err := rows.Scan(&t.TokenHash, &t.OrgID, &t.Product, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListAllProductTokens(ctx context.Context, product string) ([]ProductToken, error) {
	rows, err := s.Pool.Query(ctx, `
		select token_hash,org_id,product,created_at,last_used_at
		from billing.product_tokens
		where product=$1 and revoked_at is null
		order by created_at desc`, product)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProductToken, 0)
	for rows.Next() {
		var t ProductToken
		if err := rows.Scan(&t.TokenHash, &t.OrgID, &t.Product, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeProductToken marks one active token revoked.
//
// It is idempotent for a token that already belongs to this org/product (an
// already-revoked token is a no-op success); a hash that does not exist for this
// org/product returns ErrNotFound so a cross-org probe cannot distinguish
// "revoked elsewhere" from "never existed".
func (s *Store) RevokeProductToken(ctx context.Context, orgID, product, tokenHash string) error {
	res, err := s.Pool.Exec(ctx, `
		update billing.product_tokens set revoked_at=now()
		where token_hash=$1 and org_id=$2 and product=$3 and revoked_at is null`,
		tokenHash, orgID, product)
	if err != nil {
		return err
	}
	if res.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.Pool.QueryRow(ctx, `
		select exists(
			select 1 from billing.product_tokens
			where token_hash=$1 and org_id=$2 and product=$3
		)`, tokenHash, orgID, product).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return ErrNotFound
}

var _ = pgx.ErrNoRows
var _ = errors.Is
