package billing

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestUsageToolChargesMigration is a static guard for the Phase 2 schema. The
// repo applies migrations through Supabase, so CI has no database to inspect;
// this keeps the required columns, checks and indexes from drifting.
func TestUsageToolChargesMigration(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations", "0007_usage_tool_charges.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)

	const table = "create table if not exists billing.usage_tool_charges"
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
		"id", "org_id", "request_id", "settlement_id", "lease_id",
		"tool", "unit", "quantity", "unit_price_micro", "amount_minor",
		"provider_cost_micro", "status", "created_at",
	}
	for _, col := range columns {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(col) + `\s+`)
		if !re.MatchString(block) {
			t.Errorf("migration table is missing column %q", col)
		}
	}

	for _, check := range []string{
		"quantity > 0",
		"unit_price_micro >= 0",
		"amount_minor >= 0",
		"provider_cost_micro >= 0",
		"status in ('succeeded')",
	} {
		if !strings.Contains(block, check) {
			t.Errorf("migration table is missing check %q", check)
		}
	}

	indexes := []string{
		"usage_tool_charges_request",
		"usage_tool_charges_settlement",
		"usage_tool_charges_lease",
		"usage_tool_charges_org_created",
	}
	for _, idx := range indexes {
		if !strings.Contains(sql, "create index if not exists "+idx) {
			t.Errorf("migration is missing index %q", idx)
		}
	}

	if !strings.Contains(sql, "alter table billing.usage_tool_charges enable row level security") {
		t.Error("migration does not enable row level security")
	}
	if !strings.Contains(sql, "revoke all on billing.usage_tool_charges from anon, authenticated") {
		t.Error("migration does not revoke anon/authenticated access")
	}
}
