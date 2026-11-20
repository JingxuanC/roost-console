package billing

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCompressionPersistenceDB exercises settlement persistence, replay
// idempotency and organization isolation against a real Postgres. It is skipped
// unless ROOST_TEST_DATABASE_URL points at a database with the migrations
// applied; ordinary `go test ./...` stays dependency-free.
func TestCompressionPersistenceDB(t *testing.T) {
	databaseURL := os.Getenv("ROOST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ROOST_TEST_DATABASE_URL to run the compression database test")
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

	newOrg := func(label string) (orgID string) {
		t.Helper()
		userID := uuid.NewString()
		orgID = uuid.NewString()
		if _, err := pool.Exec(ctx, `insert into auth.users (id,email) values ($1,$2)`, userID, userID+"@test.local"); err != nil {
			t.Fatalf("%s user: %v", label, err)
		}
		if _, err := pool.Exec(ctx, `
			insert into public.organizations (id,name,slug,owner_user_id,plan_code)
			values ($1,$2,$3,$4,'free')`, orgID, "Compression "+label, "comp-"+label+"-"+userID[:8], userID); err != nil {
			t.Fatalf("%s org: %v", label, err)
		}
		if _, err := pool.Exec(ctx, `insert into billing.wallets (org_id,balance_credits) values ($1,1000)`, orgID); err != nil {
			t.Fatalf("%s wallet: %v", label, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `delete from public.organizations where id=$1`, orgID)
			_, _ = pool.Exec(context.Background(), `delete from auth.users where id=$1`, userID)
		})
		return orgID
	}

	orgA := newOrg("a")
	orgB := newOrg("b")
	orgC := newOrg("empty")

	reserve := func(orgID, requestID string) RelayReservation {
		t.Helper()
		reservation, err := store.RelayReserve(ctx, orgID, requestID, 10, RelayCurrency, time.Minute)
		if err != nil {
			t.Fatalf("reserve %s: %v", requestID, err)
		}
		return reservation
	}

	compressA := Compression{
		Applied: true, OriginalInputTokens: 33404, CompressedInputTokens: 7378,
		SavedTokens: 26026, UserShareTokens: 7807, UserShareMinor: 1542, SummaryCostMinor: 0,
	}
	compressA2 := Compression{
		Applied: true, OriginalInputTokens: 1000, CompressedInputTokens: 400,
		SavedTokens: 600, UserShareTokens: 180, UserShareMinor: 50, SummaryCostMinor: 10,
	}
	compressB := Compression{
		Applied: true, OriginalInputTokens: 20000, CompressedInputTokens: 5000,
		SavedTokens: 15000, UserShareTokens: 4500, UserShareMinor: 900, SummaryCostMinor: 20,
	}

	requestA1 := "req-" + uuid.NewString()
	leaseA1 := reserve(orgA, requestA1)
	settlementA1 := "stl-" + uuid.NewString()
	keyA1 := "settle:" + requestA1
	if op, err := store.RelaySettleWithDetails(ctx, leaseA1.ID, settlementA1, keyA1, 3, nil, &compressA, nil); err != nil {
		t.Fatalf("settle A1: %v", err)
	} else if !op.Found || op.Compression == nil || op.Compression.SavedTokens != 26026 {
		t.Fatalf("settle A1 op=%+v", op)
	}

	// A settlement without compression persists no compression row.
	requestA2 := "req-" + uuid.NewString()
	leaseA2 := reserve(orgA, requestA2)
	if _, err := store.RelaySettle(ctx, leaseA2.ID, "settle:"+requestA2, 2); err != nil {
		t.Fatalf("settle A2: %v", err)
	}

	requestA3 := "req-" + uuid.NewString()
	leaseA3 := reserve(orgA, requestA3)
	if _, err := store.RelaySettleWithDetails(ctx, leaseA3.ID, "stl-"+uuid.NewString(), "settle:"+requestA3, 1, nil, &compressA2, nil); err != nil {
		t.Fatalf("settle A3: %v", err)
	}

	requestB1 := "req-" + uuid.NewString()
	leaseB1 := reserve(orgB, requestB1)
	if _, err := store.RelaySettleWithDetails(ctx, leaseB1.ID, "stl-"+uuid.NewString(), "settle:"+requestB1, 4, nil, &compressB, nil); err != nil {
		t.Fatalf("settle B1: %v", err)
	}

	usageA, err := store.CompressionUsage(ctx, orgA, 50)
	if err != nil {
		t.Fatalf("usage A: %v", err)
	}
	if len(usageA.Rows) != 2 {
		t.Fatalf("org A rows=%d want 2: %+v", len(usageA.Rows), usageA.Rows)
	}
	// Newest first: A3 settled after A1 (ties break on a higher id).
	if usageA.Rows[0].RequestID != requestA3 || usageA.Rows[1].RequestID != requestA1 {
		t.Fatalf("org A order=%q,%q", usageA.Rows[0].RequestID, usageA.Rows[1].RequestID)
	}
	if usageA.Rows[0].OrgID != orgA || usageA.Rows[1].OrgID != orgA {
		t.Fatalf("org A rows leaked another org: %+v", usageA.Rows)
	}
	wantSaved := compressA.SavedTokens + compressA2.SavedTokens
	wantShareTokens := compressA.UserShareTokens + compressA2.UserShareTokens
	wantShareMinor := compressA.UserShareMinor + compressA2.UserShareMinor
	wantSummary := compressA.SummaryCostMinor + compressA2.SummaryCostMinor
	if usageA.Totals.Count != 2 || usageA.Totals.SavedTokens != wantSaved ||
		usageA.Totals.UserShareTokens != wantShareTokens ||
		usageA.Totals.UserShareMinor != wantShareMinor ||
		usageA.Totals.SummaryCostMinor != wantSummary {
		t.Fatalf("org A totals=%+v want count=2 saved=%d share=%d minor=%d summary=%d",
			usageA.Totals, wantSaved, wantShareTokens, wantShareMinor, wantSummary)
	}

	usageB, err := store.CompressionUsage(ctx, orgB, 50)
	if err != nil {
		t.Fatalf("usage B: %v", err)
	}
	if len(usageB.Rows) != 1 || usageB.Rows[0].RequestID != requestB1 || usageB.Rows[0].OrgID != orgB {
		t.Fatalf("org B rows=%+v", usageB.Rows)
	}
	if usageB.Totals.SavedTokens != compressB.SavedTokens || usageB.Totals.UserShareMinor != compressB.UserShareMinor {
		t.Fatalf("org B totals=%+v", usageB.Totals)
	}

	usageC, err := store.CompressionUsage(ctx, orgC, 50)
	if err != nil {
		t.Fatalf("usage C: %v", err)
	}
	if usageC.Rows == nil || len(usageC.Rows) != 0 {
		t.Fatalf("empty org rows=%v want non-nil empty", usageC.Rows)
	}
	if usageC.Totals != (CompressionTotals{}) {
		t.Fatalf("empty org totals=%+v", usageC.Totals)
	}

	// Replaying the same settlement idempotency key returns the original
	// savings and must not insert a second row or debit the wallet again.
	replay, err := store.RelaySettleWithDetails(ctx, leaseA1.ID, settlementA1, keyA1, 3, nil, &compressA, nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Compression == nil || replay.Compression.SavedTokens != compressA.SavedTokens {
		t.Fatalf("replay compression=%+v", replay.Compression)
	}
	afterReplay, err := store.CompressionUsage(ctx, orgA, 50)
	if err != nil {
		t.Fatalf("usage after replay: %v", err)
	}
	if len(afterReplay.Rows) != 2 || afterReplay.Totals != usageA.Totals {
		t.Fatalf("replay changed rows=%d totals=%+v", len(afterReplay.Rows), afterReplay.Totals)
	}
	var balanceA int64
	if err := pool.QueryRow(ctx, `select balance_credits from billing.wallets where org_id=$1`, orgA).Scan(&balanceA); err != nil {
		t.Fatalf("wallet A: %v", err)
	}
	if balanceA != 994 {
		t.Fatalf("wallet A=%d want 994 (3+2+1 debited once)", balanceA)
	}

	// A malformed compression object is rejected by the store before booking:
	// the lease stays active and neither a ledger nor a compression row exists.
	requestA4 := "req-" + uuid.NewString()
	leaseA4 := reserve(orgA, requestA4)
	bad := compressA
	bad.UserShareMinor = -1
	badKey := "settle:" + requestA4
	if _, err := store.RelaySettleWithDetails(ctx, leaseA4.ID, "stl-bad", badKey, 1, nil, &bad, nil); !errors.Is(err, ErrInvalidCompression) {
		t.Fatalf("malformed settle err=%v want ErrInvalidCompression", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `select status from billing.usage_leases where id=$1`, leaseA4.ID).Scan(&status); err != nil {
		t.Fatalf("read bad lease: %v", err)
	}
	if status != "active" {
		t.Fatalf("malformed settle changed lease status to %q", status)
	}
	var ledgerRows int64
	if err := pool.QueryRow(ctx, `select count(*) from billing.ledger where idempotency_key=$1`, badKey).Scan(&ledgerRows); err != nil {
		t.Fatalf("count bad ledger: %v", err)
	}
	if ledgerRows != 0 {
		t.Fatalf("malformed settle booked %d ledger rows", ledgerRows)
	}
}
