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

// TestRelaySettleToolChargesDB exercises the settlement transaction against a
// real Postgres. It is skipped unless ROOST_TEST_DATABASE_URL points at a
// database with the migrations applied; ordinary `go test ./...` stays
// dependency-free.
func TestRelaySettleToolChargesDB(t *testing.T) {
	databaseURL := os.Getenv("ROOST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ROOST_TEST_DATABASE_URL to run the tool-charge database test")
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
	store := New(pool)

	userID := uuid.NewString()
	orgID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into auth.users (id,email) values ($1,$2)`, userID, userID+"@test.local"); err != nil {
		pool.Close()
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from public.organizations where id=$1`, orgID)
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id=$1`, userID)
		pool.Close()
	})
	if _, err := pool.Exec(ctx, `
		insert into public.organizations (id,name,slug,owner_user_id,plan_code)
		values ($1,'Tool Charge Test',$2,$3,'free')`, orgID, "tool-test-"+userID[:8], userID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into billing.wallets (org_id,balance_credits) values ($1,1000)`, orgID); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	reserve := func(requestID string) RelayReservation {
		t.Helper()
		reservation, err := store.RelayReserve(ctx, orgID, requestID, 10, RelayCurrency, time.Minute)
		if err != nil {
			t.Fatalf("reserve %s: %v", requestID, err)
		}
		return reservation
	}

	requestID := "req-" + uuid.NewString()
	reservation := reserve(requestID)
	settlementID := "stl-" + uuid.NewString()
	idempotencyKey := "settle:" + requestID
	charges := []ToolCharge{
		{Tool: "search", Unit: "call", Quantity: 1, UnitPriceMicro: 700000, AmountMinor: 7, ProviderCostMicro: 750, Status: ToolChargeStatusSucceeded},
		{Tool: "fetch", Unit: "page", Quantity: 2, UnitPriceMicro: 50000, AmountMinor: 100, ProviderCostMicro: 120, Status: ToolChargeStatusSucceeded},
	}

	op, err := store.RelaySettleWithCharges(ctx, reservation.ID, settlementID, idempotencyKey, 3, charges)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if !op.Found || op.Amount != 3 || len(op.ToolCharges) != 2 {
		t.Fatalf("settle op=%+v", op)
	}

	var rows, amountMinor, providerCost int64
	if err := pool.QueryRow(ctx, `
		select count(*),coalesce(sum(amount_minor),0),coalesce(sum(provider_cost_micro),0)
		from billing.usage_tool_charges where lease_id=$1`, reservation.ID).
		Scan(&rows, &amountMinor, &providerCost); err != nil {
		t.Fatalf("count charges: %v", err)
	}
	if rows != 2 || amountMinor != 107 || providerCost != 870 {
		t.Fatalf("persisted rows=%d amount=%d provider=%d", rows, amountMinor, providerCost)
	}
	var gotRequestID, gotSettlementID string
	if err := pool.QueryRow(ctx, `
		select request_id,settlement_id from billing.usage_tool_charges
		where lease_id=$1 order by id limit 1`, reservation.ID).Scan(&gotRequestID, &gotSettlementID); err != nil {
		t.Fatalf("read identifiers: %v", err)
	}
	if gotRequestID != requestID || gotSettlementID != settlementID {
		t.Fatalf("identifiers request=%q settlement=%q", gotRequestID, gotSettlementID)
	}

	// The admin ledger is the existing reporting surface; relay rows must carry
	// the breakdown and totals for the billing page's "工具调用" line.
	ledger, err := store.AdminLedger(ctx, orgID, 20)
	if err != nil {
		t.Fatalf("admin ledger: %v", err)
	}
	var relayRow map[string]any
	for _, row := range ledger {
		if row["reason"] == "relay_usage" && row["ref_id"] == reservation.ID {
			relayRow = row
			break
		}
	}
	if relayRow == nil {
		t.Fatalf("admin ledger has no relay row: %+v", ledger)
	}
	if relayRow["tool_charges_amount_minor"] != int64(107) || relayRow["tool_charges_provider_cost_micro"] != int64(870) {
		t.Fatalf("admin ledger totals=%+v", relayRow)
	}
	if charges, ok := relayRow["tool_charges"].([]ToolCharge); !ok || len(charges) != 2 {
		t.Fatalf("admin ledger charges=%+v", relayRow["tool_charges"])
	}

	// Both reporting reads return the same persisted breakdown.
	if charges, err := store.RelayToolCharges(ctx, reservation.ID); err != nil || len(charges) != 2 {
		t.Fatalf("reservation tool charges=%+v err=%v", charges, err)
	}
	if replay, err := store.RelayOperation(ctx, idempotencyKey); err != nil || len(replay.ToolCharges) != 2 {
		t.Fatalf("operation replay=%+v err=%v", replay, err)
	}

	// A replay with the same idempotency key returns the original booking and
	// must not insert a second breakdown.
	replay, err := store.RelaySettleWithCharges(ctx, reservation.ID, settlementID, idempotencyKey, 3, charges)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Found || replay.Amount != 3 || len(replay.ToolCharges) != 2 {
		t.Fatalf("replay op=%+v", replay)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from billing.usage_tool_charges where lease_id=$1`, reservation.ID).Scan(&rows); err != nil {
		t.Fatalf("count after replay: %v", err)
	}
	if rows != 2 {
		t.Fatalf("replay inserted rows: %d", rows)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `select balance_credits from billing.wallets where org_id=$1`, orgID).Scan(&balance); err != nil {
		t.Fatalf("read wallet: %v", err)
	}
	if balance != 997 {
		t.Fatalf("wallet debited more than once: %d", balance)
	}

	// Without tool_charges the old behavior is unchanged: the settlement is
	// booked, but there is no breakdown.
	plainRequestID := "req-" + uuid.NewString()
	plainReservation := reserve(plainRequestID)
	plain, err := store.RelaySettle(ctx, plainReservation.ID, "settle:"+plainRequestID, 2)
	if err != nil {
		t.Fatalf("plain settle: %v", err)
	}
	if !plain.Found || plain.Amount != 2 || len(plain.ToolCharges) != 0 {
		t.Fatalf("plain op=%+v", plain)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from billing.usage_tool_charges where lease_id=$1`, plainReservation.ID).Scan(&rows); err != nil {
		t.Fatalf("count plain: %v", err)
	}
	if rows != 0 {
		t.Fatalf("plain settle persisted %d tool rows", rows)
	}

	// A malformed breakdown is rejected by the store before booking: the lease
	// stays active and no ledger row exists.
	badRequestID := "req-" + uuid.NewString()
	badReservation := reserve(badRequestID)
	badKey := "settle:" + badRequestID
	bad := []ToolCharge{{Tool: "search", Unit: "call", Quantity: 0, Status: ToolChargeStatusSucceeded}}
	if _, err := store.RelaySettleWithCharges(ctx, badReservation.ID, "", badKey, 1, bad); !errors.Is(err, ErrInvalidToolCharges) {
		t.Fatalf("malformed settle err=%v want ErrInvalidToolCharges", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `select status from billing.usage_leases where id=$1`, badReservation.ID).Scan(&status); err != nil {
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
