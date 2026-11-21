package billing

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestSandboxUsageMigration is a static guard for the sandbox billing schema.
// The repo applies migrations through Supabase, so CI has no database to
// inspect; this keeps the required columns, checks, indexes, comments and
// grants from drifting.
func TestSandboxUsageMigration(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations", "0010_sandbox_usage.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)

	const table = "create table if not exists billing.sandbox_usage"
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
		"id", "org_id", "user_id", "product", "sandbox_id", "provider",
		"provider_sandbox_id", "run_id", "started_at", "ended_at",
		"billable_seconds", "cpu_seconds", "memory_mib_seconds", "disk_mib_seconds",
		"gpu_seconds", "cpu_credits", "memory_credits", "disk_credits", "gpu_credits",
		"total_credits", "credits_minor", "currency", "unit_prices",
		"idempotency_key", "created_at",
	}
	for _, col := range columns {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(col) + `\s+`)
		if !re.MatchString(block) {
			t.Errorf("migration table is missing column %q", col)
		}
	}

	for _, check := range []string{
		"billable_seconds >= 0",
		"cpu_seconds >= 0",
		"memory_mib_seconds >= 0",
		"disk_mib_seconds >= 0",
		"gpu_seconds >= 0",
		"cpu_credits >= 0",
		"memory_credits >= 0",
		"disk_credits >= 0",
		"gpu_credits >= 0",
		"total_credits >= 0",
		"credits_minor >= 0",
		"currency text not null default 'CNY'",
	} {
		if !strings.Contains(block, check) {
			t.Errorf("migration table is missing %q", check)
		}
	}

	// Idempotency is keyed by the product-supplied key: a replay cannot insert a
	// second row or debit twice.
	if !strings.Contains(block, "idempotency_key text not null unique") {
		t.Error("migration is missing the unique idempotency key")
	}

	for _, idx := range []string{"sandbox_usage_org_created", "sandbox_usage_org_sandbox"} {
		if !strings.Contains(sql, "create index if not exists "+idx) {
			t.Errorf("migration is missing index %q", idx)
		}
	}
	if !strings.Contains(sql, "on billing.sandbox_usage(org_id, created_at desc)") {
		t.Error("migration is missing the org/created_at index definition")
	}
	if !strings.Contains(sql, "on billing.sandbox_usage(org_id, sandbox_id)") {
		t.Error("migration is missing the org/sandbox_id index definition")
	}

	if !strings.Contains(sql, "comment on table billing.sandbox_usage") {
		t.Error("migration does not comment the table")
	}
	if !strings.Contains(sql, "comment on column billing.sandbox_usage.credits_minor") {
		t.Error("migration does not comment the minor-unit convention")
	}

	if !strings.Contains(sql, "alter table billing.sandbox_usage enable row level security") {
		t.Error("migration does not enable row level security")
	}
	if !strings.Contains(sql, "revoke all on billing.sandbox_usage from anon, authenticated") {
		t.Error("migration does not revoke anon/authenticated access")
	}
	if !strings.Contains(sql, "revoke all on sequence billing.sandbox_usage_id_seq from anon, authenticated") {
		t.Error("migration does not revoke anon/authenticated sequence access")
	}
	if !strings.Contains(sql, "grant select, insert, update, delete on billing.sandbox_usage to roost_api") {
		t.Error("migration does not grant the application role table access")
	}
	if !strings.Contains(sql, "grant usage, select on sequence billing.sandbox_usage_id_seq to roost_api") {
		t.Error("migration does not grant the application role sequence access")
	}
}
