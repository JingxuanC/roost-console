package billing

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestReferralsMigration is a static guard for the referral schema. The repo
// applies migrations through Supabase, so CI has no database to inspect; this
// keeps the columns, uniqueness, indexes and grants from drifting.
func TestReferralsMigration(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations", "0011_referrals.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)

	codeBlock := tableBlock(t, sql, "create table if not exists billing.referral_codes")
	for _, col := range []string{
		"code", "owner_user_id", "owner_org_id", "active", "uses", "max_uses", "created_at",
	} {
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(col) + `\s+`).MatchString(codeBlock) {
			t.Errorf("referral_codes is missing column %q", col)
		}
	}
	for _, check := range []string{
		"uses integer not null default 0 check (uses >= 0)",
		"max_uses integer not null default 0 check (max_uses >= 0)",
		"active boolean not null default true",
		"owner_user_id uuid not null references auth.users",
		"owner_org_id uuid not null references public.organizations",
	} {
		if !strings.Contains(codeBlock, check) {
			t.Errorf("referral_codes is missing %q", check)
		}
	}

	referralBlock := tableBlock(t, sql, "create table if not exists billing.referrals")
	for _, col := range []string{
		"id", "code", "inviter_user_id", "inviter_org_id",
		"invitee_user_id", "invitee_org_id", "status",
		"inviter_credits", "invitee_credits", "created_at", "rewarded_at",
	} {
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(col) + `\s+`).MatchString(referralBlock) {
			t.Errorf("referrals is missing column %q", col)
		}
	}
	for _, want := range []string{
		"id uuid primary key",
		"invitee_user_id uuid not null unique",
		"status text not null default 'rewarded' check (status in ('rewarded'))",
		"inviter_credits integer not null default 0 check (inviter_credits >= 0)",
		"invitee_credits integer not null default 0 check (invitee_credits >= 0)",
		"code text not null references billing.referral_codes(code)"} {
		if !strings.Contains(referralBlock, want) {
			t.Errorf("referrals is missing %q", want)
		}
	}

	for _, idx := range []string{
		"create unique index if not exists referral_codes_owner_org on billing.referral_codes(owner_org_id)",
		"create index if not exists referrals_code on billing.referrals(code)",
		"create index if not exists referrals_inviter on billing.referrals(inviter_org_id, created_at desc)",
	} {
		if !strings.Contains(sql, idx) {
			t.Errorf("migration is missing %q", idx)
		}
	}

	for _, table := range []string{"billing.referral_codes", "billing.referrals"} {
		if !strings.Contains(sql, "alter table "+table+" enable row level security") {
			t.Errorf("migration does not enable row level security on %s", table)
		}
		if !strings.Contains(sql, "revoke all on "+table+" from anon, authenticated") {
			t.Errorf("migration does not revoke anon/authenticated access on %s", table)
		}
		if !strings.Contains(sql, "grant select, insert, update, delete on "+table+" to roost_api") {
			t.Errorf("migration does not grant the application role access on %s", table)
		}
	}

	// Gifts additionally audit the operator + note in billing.admin_audit.
	if !strings.Contains(sql, "grant select, insert on billing.admin_audit to roost_api") {
		t.Error("migration does not grant admin_audit access for gift auditing")
	}
	if !strings.Contains(sql, "grant usage, select on sequence billing.admin_audit_id_seq to roost_api") {
		t.Error("migration does not grant admin_audit sequence access for gift auditing")
	}
}

func tableBlock(t *testing.T, sql, marker string) string {
	t.Helper()
	start := strings.Index(sql, marker)
	if start < 0 {
		t.Fatalf("migration is missing %q", marker)
	}
	end := strings.Index(sql[start:], ");")
	if end < 0 {
		t.Fatalf("migration table %q is not terminated", marker)
	}
	return sql[start : start+end]
}
