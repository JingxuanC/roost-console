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

// TestUsageEventsPersistenceDB exercises usage-event persistence, replay
// idempotency, per-organization isolation and the reporting queries against a
// real Postgres. It is skipped unless ROOST_TEST_DATABASE_URL points at a
// database with the migrations applied; ordinary `go test ./...` stays
// dependency-free.
func TestUsageEventsPersistenceDB(t *testing.T) {
	databaseURL := os.Getenv("ROOST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ROOST_TEST_DATABASE_URL to run the usage database test")
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
			values ($1,$2,$3,$4,'free')`, orgID, "Usage "+label, "usage-"+label+"-"+userID[:8], userID); err != nil {
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

	usageA1 := UsageEvent{
		Model: " deepseek-v4-pro ", Requests: 1, InputTokens: 33404, OutputTokens: 256,
		CacheReadTokens: 8192, CacheWriteTokens: 0, UpstreamCostMinor: int64Ptr(1616),
	}
	usageA2 := UsageEvent{
		// Requests is omitted: the store records the default of one request.
		Model: "deepseek-v4-flash", InputTokens: 1000, OutputTokens: 20,
		CacheReadTokens: 100, CacheWriteTokens: 50,
	}
	compressA1 := Compression{
		Applied: true, OriginalInputTokens: 33404, CompressedInputTokens: 7378,
		SavedTokens: 26026, UserShareTokens: 7807, UserShareMinor: 1542, SummaryCostMinor: 12,
	}
	usageB1 := UsageEvent{
		Model: "deepseek-v4-pro", Requests: 1, InputTokens: 20000, OutputTokens: 400,
		CacheReadTokens: 5000, CacheWriteTokens: 0, UpstreamCostMinor: int64Ptr(900),
	}

	requestA1 := "req-" + uuid.NewString()
	leaseA1 := reserve(orgA, requestA1)
	settlementA1 := "stl-" + uuid.NewString()
	keyA1 := "settle:" + requestA1
	if op, err := store.RelaySettleWithDetails(ctx, leaseA1.ID, settlementA1, keyA1, 3, nil, &compressA1, &usageA1); err != nil {
		t.Fatalf("settle A1: %v", err)
	} else if op.Usage == nil || op.Usage.Model != "deepseek-v4-pro" || op.Usage.InputTokens != 33404 {
		t.Fatalf("settle A1 usage=%+v", op.Usage)
	}

	requestA2 := "req-" + uuid.NewString()
	leaseA2 := reserve(orgA, requestA2)
	if _, err := store.RelaySettleWithDetails(ctx, leaseA2.ID, "stl-"+uuid.NewString(), "settle:"+requestA2, 2, nil, nil, &usageA2); err != nil {
		t.Fatalf("settle A2: %v", err)
	}

	// A settlement without a usage object persists no usage row, while its
	// ledger charge still counts toward spend.
	requestA3 := "req-" + uuid.NewString()
	leaseA3 := reserve(orgA, requestA3)
	if _, err := store.RelaySettle(ctx, leaseA3.ID, "settle:"+requestA3, 1); err != nil {
		t.Fatalf("settle A3: %v", err)
	}

	requestB1 := "req-" + uuid.NewString()
	leaseB1 := reserve(orgB, requestB1)
	if _, err := store.RelaySettleWithDetails(ctx, leaseB1.ID, "stl-"+uuid.NewString(), "settle:"+requestB1, 4, nil, nil, &usageB1); err != nil {
		t.Fatalf("settle B1: %v", err)
	}

	// Replaying the same settlement must not insert another usage row or debit
	// the wallet again.
	replay, err := store.RelaySettleWithDetails(ctx, leaseA1.ID, settlementA1, keyA1, 3, nil, &compressA1, &usageA1)
	if err != nil {
		t.Fatalf("replay A1: %v", err)
	}
	if replay.Usage == nil || replay.Usage.InputTokens != usageA1.InputTokens {
		t.Fatalf("replay usage=%+v", replay.Usage)
	}

	summaryA, err := store.UsageSummary(ctx, orgA, 30)
	if err != nil {
		t.Fatalf("summary A: %v", err)
	}
	wantA := UsageTotals{
		Requests:          2,
		InputTokens:       34404,
		OutputTokens:      276,
		CacheReadTokens:   8292,
		CacheWriteTokens:  50,
		TotalTokens:       43022,
		CreditsMinor:      6000,
		UpstreamCostMinor: 1616,
		SavedTokens:       26026,
		UserShareTokens:   7807,
		UserShareMinor:    1542,
		SummaryCostMinor:  12,
	}
	wantA.CacheHitRate = cacheHitRate(wantA.CacheReadTokens, wantA.InputTokens)
	if summaryA.Days != 30 || summaryA.Totals != wantA {
		t.Fatalf("summary A=%+v want=%+v", summaryA, wantA)
	}

	dailyA, err := store.UsageDaily(ctx, orgA, 30)
	if err != nil {
		t.Fatalf("daily A: %v", err)
	}
	if len(dailyA) == 0 {
		t.Fatal("daily A is empty")
	}
	for i := 1; i < len(dailyA); i++ {
		if dailyA[i-1].Date >= dailyA[i].Date {
			t.Fatalf("daily A is not oldest-first: %q then %q", dailyA[i-1].Date, dailyA[i].Date)
		}
	}
	var dailyTotals UsageDailyRow
	for _, row := range dailyA {
		dailyTotals.Requests += row.Requests
		dailyTotals.InputTokens += row.InputTokens
		dailyTotals.OutputTokens += row.OutputTokens
		dailyTotals.CacheReadTokens += row.CacheReadTokens
		dailyTotals.CacheWriteTokens += row.CacheWriteTokens
		dailyTotals.TotalTokens += row.TotalTokens
		dailyTotals.CreditsMinor += row.CreditsMinor
		dailyTotals.SavedTokens += row.SavedTokens
		dailyTotals.UserShareTokens += row.UserShareTokens
		dailyTotals.UserShareMinor += row.UserShareMinor
	}
	if dailyTotals.Requests != wantA.Requests || dailyTotals.InputTokens != wantA.InputTokens ||
		dailyTotals.TotalTokens != wantA.TotalTokens || dailyTotals.CreditsMinor != wantA.CreditsMinor ||
		dailyTotals.SavedTokens != wantA.SavedTokens || dailyTotals.UserShareMinor != wantA.UserShareMinor {
		t.Fatalf("daily totals=%+v want=%+v", dailyTotals, wantA)
	}

	recentA, err := store.UsageRecent(ctx, orgA, 50)
	if err != nil {
		t.Fatalf("recent A: %v", err)
	}
	if len(recentA) != 2 {
		t.Fatalf("recent A rows=%d: %+v", len(recentA), recentA)
	}
	if recentA[0].RequestID != requestA2 || recentA[1].RequestID != requestA1 {
		t.Fatalf("recent A order=%q,%q", recentA[0].RequestID, recentA[1].RequestID)
	}
	if recentA[0].Requests != 1 {
		t.Fatalf("default requests=%d want 1", recentA[0].Requests)
	}
	if recentA[0].CreditsMinor != 2000 || recentA[1].CreditsMinor != 3000 {
		t.Fatalf("recent credits=%d,%d", recentA[0].CreditsMinor, recentA[1].CreditsMinor)
	}
	if !recentA[1].CompressionApplied || recentA[1].OriginalInputTokens != 33404 ||
		recentA[1].CompressedInputTokens != 7378 || recentA[1].SavedTokens != 26026 ||
		recentA[1].UserShareTokens != 7807 || recentA[1].UserShareMinor != 1542 {
		t.Fatalf("recent compression=%+v", recentA[1])
	}
	if recentA[0].TotalTokens != 1170 || recentA[0].CacheHitRate != float64(100)/float64(1100) {
		t.Fatalf("recent A2 tokens/rate=%+v", recentA[0])
	}

	// The replay above must not have inserted a second event row.
	var eventRowsA, eventRowsB int64
	if err := pool.QueryRow(ctx, `select count(*) from billing.usage_events where org_id=$1`, orgA).Scan(&eventRowsA); err != nil {
		t.Fatalf("count usage A: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from billing.usage_events where org_id=$1`, orgB).Scan(&eventRowsB); err != nil {
		t.Fatalf("count usage B: %v", err)
	}
	if eventRowsA != 2 || eventRowsB != 1 {
		t.Fatalf("usage rows A=%d B=%d want 2/1", eventRowsA, eventRowsB)
	}

	summaryB, err := store.UsageSummary(ctx, orgB, 30)
	if err != nil {
		t.Fatalf("summary B: %v", err)
	}
	if summaryB.Totals.Requests != 1 || summaryB.Totals.InputTokens != 20000 ||
		summaryB.Totals.CreditsMinor != 4000 || summaryB.Totals.UpstreamCostMinor != 900 {
		t.Fatalf("summary B=%+v", summaryB)
	}
	recentB, err := store.UsageRecent(ctx, orgB, 50)
	if err != nil {
		t.Fatalf("recent B: %v", err)
	}
	if len(recentB) != 1 || recentB[0].RequestID != requestB1 {
		t.Fatalf("recent B=%+v", recentB)
	}

	// Empty organization: all-zero summary and non-nil empty arrays.
	summaryC, err := store.UsageSummary(ctx, orgC, 30)
	if err != nil {
		t.Fatalf("summary C: %v", err)
	}
	if summaryC.Totals != (UsageTotals{}) {
		t.Fatalf("empty summary=%+v", summaryC.Totals)
	}
	dailyC, err := store.UsageDaily(ctx, orgC, 30)
	if err != nil {
		t.Fatalf("daily C: %v", err)
	}
	if dailyC == nil || len(dailyC) != 0 {
		t.Fatalf("empty daily=%v want non-nil empty", dailyC)
	}
	recentC, err := store.UsageRecent(ctx, orgC, 50)
	if err != nil {
		t.Fatalf("recent C: %v", err)
	}
	if recentC == nil || len(recentC) != 0 {
		t.Fatalf("empty recent=%v want non-nil empty", recentC)
	}

	// A malformed usage object is rejected by the store before booking: the
	// lease stays active and neither a ledger nor a usage row exists.
	requestA4 := "req-" + uuid.NewString()
	leaseA4 := reserve(orgA, requestA4)
	bad := usageA1
	bad.OutputTokens = -1
	badKey := "settle:" + requestA4
	if _, err := store.RelaySettleWithDetails(ctx, leaseA4.ID, "stl-bad", badKey, 1, nil, nil, &bad); !errors.Is(err, ErrInvalidUsage) {
		t.Fatalf("malformed settle err=%v want ErrInvalidUsage", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `select status from billing.usage_leases where id=$1`, leaseA4.ID).Scan(&status); err != nil {
		t.Fatalf("read bad lease: %v", err)
	}
	if status != "active" {
		t.Fatalf("malformed settle changed lease status to %q", status)
	}
	var ledgerRows, usageRows int64
	if err := pool.QueryRow(ctx, `select count(*) from billing.ledger where idempotency_key=$1`, badKey).Scan(&ledgerRows); err != nil {
		t.Fatalf("count bad ledger: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from billing.usage_events where lease_id=$1`, leaseA4.ID).Scan(&usageRows); err != nil {
		t.Fatalf("count bad usage: %v", err)
	}
	if ledgerRows != 0 || usageRows != 0 {
		t.Fatalf("malformed settle booked ledger=%d usage=%d", ledgerRows, usageRows)
	}
}
