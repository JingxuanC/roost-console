package billing

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestProductTokenUserBindingDB exercises 0012 against a real Postgres: a
// console-issued relay token records its user, introspect returns it, and a
// legacy token (or service-issued token) stays NULL and user-less. It is skipped
// unless ROOST_TEST_DATABASE_URL points at a database with the migrations
// applied.
func TestProductTokenUserBindingDB(t *testing.T) {
	databaseURL := os.Getenv("ROOST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ROOST_TEST_DATABASE_URL to run the product token user test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	store := New(pool)

	userID := uuid.NewString()
	orgID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into auth.users (id,email) values ($1,$2)`, userID, userID+"@test.local"); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into public.organizations (id,name,slug,owner_user_id,plan_code)
		values ($1,$2,$3,$4,'free')`, orgID, "Token user test", "token-user-"+userID[:8], userID); err != nil {
		t.Fatalf("org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from public.organizations where id=$1`, orgID)
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id=$1`, userID)
	})

	// A user-session issuance is bound to that user and introspect reports it.
	token, meta, err := store.IssueProductToken(ctx, orgID, RelayProduct, userID)
	if err != nil {
		t.Fatalf("IssueProductToken user: %v", err)
	}
	if meta.CreatedByUserID == nil || *meta.CreatedByUserID != userID {
		t.Fatalf("issued token created_by_user_id = %v, want %s", meta.CreatedByUserID, userID)
	}
	identity, err := store.RelayAPIKey(ctx, token)
	if err != nil {
		t.Fatalf("RelayAPIKey user: %v", err)
	}
	if identity.OrgID != orgID || identity.UserID != userID {
		t.Fatalf("identity = %+v, want org %s user %s", identity, orgID, userID)
	}

	// No user session (service issuance) stores NULL, the legacy shape.
	legacyToken, legacyMeta, err := store.IssueProductToken(ctx, orgID, RelayProduct, "")
	if err != nil {
		t.Fatalf("IssueProductToken legacy: %v", err)
	}
	if legacyMeta.CreatedByUserID != nil {
		t.Fatalf("legacy token created_by_user_id = %v, want NULL", legacyMeta.CreatedByUserID)
	}
	legacyIdentity, err := store.RelayAPIKey(ctx, legacyToken)
	if err != nil {
		t.Fatalf("RelayAPIKey legacy: %v", err)
	}
	if legacyIdentity.OrgID != orgID || legacyIdentity.UserID != "" {
		t.Fatalf("legacy identity = %+v, want org %s empty user", legacyIdentity, orgID)
	}

	// The listing exposes the same distinction for the console UI.
	tokens, err := store.ListProductTokens(ctx, orgID, RelayProduct)
	if err != nil {
		t.Fatalf("ListProductTokens: %v", err)
	}
	var sawUser, sawLegacy bool
	for _, item := range tokens {
		switch item.TokenHash {
		case meta.TokenHash:
			sawUser = item.CreatedByUserID != nil && *item.CreatedByUserID == userID
		case legacyMeta.TokenHash:
			sawLegacy = item.CreatedByUserID == nil
		}
	}
	if !sawUser || !sawLegacy {
		t.Fatalf("listing user=%v legacy=%v tokens=%+v", sawUser, sawLegacy, tokens)
	}
}
