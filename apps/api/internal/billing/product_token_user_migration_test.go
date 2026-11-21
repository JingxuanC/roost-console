package billing

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProductTokenUserMigration is a static guard for 0012: CI applies
// migrations through Supabase and has no database to inspect. It pins the
// nullable user binding and the lookup index without requiring Postgres.
func TestProductTokenUserMigration(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations", "0012_product_token_user.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)
	lower := strings.ToLower(sql)
	compact := strings.Join(strings.Fields(lower), " ")

	for _, want := range []string{
		"alter table billing.product_tokens",
		"add column if not exists created_by_user_id uuid",
		"references auth.users(id)",
		"on delete set null",
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("migration is missing %q", want)
		}
	}

	if !strings.Contains(compact, "create index if not exists product_tokens_org_product_user") {
		t.Error("migration is missing the (org_id, product, created_by_user_id) index")
	}
	if !strings.Contains(compact, "on billing.product_tokens(org_id, product, created_by_user_id)") {
		t.Error("migration index does not cover (org_id, product, created_by_user_id)")
	}

	// The new column must not widen access. RLS and the anon/authenticated
	// revokes from 0006 remain the only policy for this table.
	for _, forbidden := range []string{
		"grant ",
		"drop policy",
		"disable row level security",
		"no force row level security",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("migration changes access policy: contains %q", forbidden)
		}
	}
}
