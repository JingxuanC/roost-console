package billing

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestUsageCompressionMigration is a static guard for the compression schema.
// The repo applies migrations through Supabase, so CI has no database to
// inspect; this keeps the required columns, checks, indexes and grants from
// drifting.
func TestUsageCompressionMigration(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations", "0008_usage_compression.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)

	const table = "create table if not exists billing.usage_compression"
	start := strings.Index(sql, table)
	if start < 0 {
		t.Fatalf("migration is missing %q", table)
	}
	end := strings.Index(sql[start:], ");")
	if end < 0 {
		t.Fatal("migration table definition is not terminated")
	}
	block := sql[start : start+end]

	columns := []string{
		"id", "org_id", "request_id", "settlement_id", "lease_id", "applied",
		"original_input_tokens", "compressed_input_tokens", "saved_tokens",
		"user_share_tokens", "user_share_minor", "summary_cost_minor", "created_at",
	}
	for _, col := range columns {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(col) + `\s+`)
		if !re.MatchString(block) {
			t.Errorf("migration table is missing column %q", col)
		}
	}

	for _, check := range []string{
		"original_input_tokens >= 0",
		"compressed_input_tokens >= 0",
		"saved_tokens >= 0",
		"user_share_tokens >= 0",
		"user_share_minor >= 0",
		"summary_cost_minor >= 0",
	} {
		if !strings.Contains(block, check) {
			t.Errorf("migration table is missing check %q", check)
		}
	}

	// Idempotency is keyed by the relay's settlement id: a replay cannot insert
	// a second row.
	if !strings.Contains(block, "unique (settlement_id)") {
		t.Error("migration table is missing unique (settlement_id)")
	}

	for _, idx := range []string{
		"usage_compression_org_created",
		"usage_compression_lease",
		"usage_compression_request",
	} {
		if !strings.Contains(sql, "create index if not exists "+idx) {
			t.Errorf("migration is missing index %q", idx)
		}
	}
	if !strings.Contains(sql, "on billing.usage_compression(org_id, created_at desc)") {
		t.Error("migration is missing the org/created_at index definition")
	}

	if !strings.Contains(sql, "alter table billing.usage_compression enable row level security") {
		t.Error("migration does not enable row level security")
	}
	if !strings.Contains(sql, "revoke all on billing.usage_compression from anon, authenticated") {
		t.Error("migration does not revoke anon/authenticated access")
	}
	if !strings.Contains(sql, "revoke all on sequence billing.usage_compression_id_seq from anon, authenticated") {
		t.Error("migration does not revoke anon/authenticated sequence access")
	}
	if !strings.Contains(sql, "grant select, insert, update, delete on billing.usage_compression to roost_api") {
		t.Error("migration does not grant the application role table access")
	}
	if !strings.Contains(sql, "grant usage, select on sequence billing.usage_compression_id_seq to roost_api") {
		t.Error("migration does not grant the application role sequence access")
	}
}
