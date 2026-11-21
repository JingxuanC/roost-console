package billing

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAdminOverviewDB exercises the cross-organization platform overview
// against a real Postgres. It is skipped unless ROOST_TEST_DATABASE_URL points
// at a database with the migrations applied, matching the other gated DB tests.
func TestAdminOverviewDB(t *testing.T) {
	databaseURL := os.Getenv("ROOST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ROOST_TEST_DATABASE_URL to run the admin overview database test")
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

	// Global aggregates include whatever the test database already holds, so
	// the assertions below compare against a baseline taken before the fixture
	// is inserted and scope per-org checks by id.
	before, err := store.AdminOverview(ctx, 30)
	if err != nil {
		t.Fatalf("baseline overview: %v", err)
	}

	newUser := func(label string) (string, string) {
		t.Helper()
		id := uuid.NewString()
		email := label + "-" + id[:8] + "@test.local"
		if _, err := pool.Exec(ctx, `insert into auth.users (id,email) values ($1,$2)`, id, email); err != nil {
			t.Fatalf("%s user: %v", label, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `delete from auth.users where id=$1`, id)
		})
		return id, email
	}
	newOrg := func(label, ownerID, plan string) string {
		t.Helper()
		orgID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			insert into public.organizations (id,name,slug,owner_user_id,plan_code)
			values ($1,$2,$3,$4,$5)`,
			orgID, "Overview "+label, "overview-"+label+"-"+orgID[:8], ownerID, plan); err != nil {
			t.Fatalf("%s org: %v", label, err)
		}
		if _, err := pool.Exec(ctx, `insert into billing.wallets (org_id,balance_credits) values ($1,$2)`, orgID, 5000); err != nil {
			t.Fatalf("%s wallet: %v", label, err)
		}
		if _, err := pool.Exec(ctx, `insert into public.organization_members (org_id,user_id,role) values ($1,$2,'owner')`, orgID, ownerID); err != nil {
			t.Fatalf("%s member: %v", label, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `delete from public.organizations where id=$1`, orgID)
		})
		return orgID
	}
	insertLedger := func(orgID, product, reason string, delta int64, age time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			insert into billing.ledger (org_id,product,delta_credits,reason,created_at)
			values ($1,$2,$3,$4,now() - make_interval(secs => $5))`,
			orgID, product, delta, reason, int64(age.Seconds())); err != nil {
			t.Fatalf("ledger %s/%s/%d: %v", product, reason, delta, err)
		}
	}
	insertLease := func(orgID string) string {
		t.Helper()
		var leaseID string
		if err := pool.QueryRow(ctx, `
			insert into billing.usage_leases (org_id,product,run_ref,reserved_credits,status,expires_at)
			values ($1,'relay',$2,1000,'settled',now() + interval '1 hour')
			returning id`, orgID, "overview-"+uuid.NewString()).Scan(&leaseID); err != nil {
			t.Fatalf("lease: %v", err)
		}
		return leaseID
	}
	insertUsage := func(orgID, leaseID string, input, output, cacheRead, cacheWrite int64, age time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			insert into billing.usage_events
				(org_id,request_id,settlement_id,lease_id,model,requests,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,created_at)
			values ($1,$2,$3,$4,'deepseek-v4-pro',1,$5,$6,$7,$8,now() - make_interval(secs => $9))`,
			orgID, "req-"+uuid.NewString(), "stl-"+uuid.NewString(), leaseID,
			input, output, cacheRead, cacheWrite, int64(age.Seconds())); err != nil {
			t.Fatalf("usage event: %v", err)
		}
	}
	insertOrder := func(orgID, status string, amountCents int64, age time.Duration) string {
		t.Helper()
		id := uuid.NewString()
		var paidAt *time.Time
		if status == "paid" || status == "fulfilled" {
			paid := time.Now().UTC().Add(-age)
			paidAt = &paid
		}
		if _, err := pool.Exec(ctx, `
			insert into billing.orders
				(id,org_id,out_trade_no,sku,amount_cents,credits,status,paid_at,created_at)
			values ($1,$2,$3,'credit_100',$4,10000,$5,$6,now() - make_interval(secs => $7))`,
			id, orgID, uuid.NewString()[:24], amountCents, status, paidAt, int64(age.Seconds())); err != nil {
			t.Fatalf("order %s: %v", status, err)
		}
		return id
	}

	userA, emailA := newUser("overview-a")
	userB, emailB := newUser("overview-b")
	orgA := newOrg("a", userA, "pro")
	orgB := newOrg("b", userB, "free")

	// org A consumes across all four products. Adjustments, grants and payments
	// must not count as consumption even though they move the ledger.
	insertLedger(orgA, "relay", "usage", -5_000_000, 0)
	insertLedger(orgA, "relay", "relay_usage", -100, 0)
	insertLedger(orgA, "market", "usage", -200, 0)
	insertLedger(orgA, "causal-memory", "usage", -50, 0)
	insertLedger(orgA, "dslpha", "usage", -70, 0)
	insertLedger(orgA, "relay", "admin_adjust", -1_000_000, 0)
	insertLedger(orgA, "", "free_grant", 1_000_000, 0)
	insertLedger(orgA, "", "payment", 500_000, 0)
	// Outside the 30-day window: excluded from totals and daily buckets.
	insertLedger(orgA, "relay", "usage", -999_999, 40*24*time.Hour)

	// Orders in the range: two revenue-bearing, one pending. A refund on org B
	// counts as an order but not as revenue.
	orderA1 := insertOrder(orgA, "paid", 10_000, time.Minute)
	orderA2 := insertOrder(orgA, "fulfilled", 5_000, 2*time.Minute)
	orderA3 := insertOrder(orgA, "pending", 3_000, 3*time.Minute)
	orderB1 := insertOrder(orgB, "refunded", 9_999, 4*time.Minute)

	leaseA1 := insertLease(orgA)
	leaseA2 := insertLease(orgA)
	leaseB1 := insertLease(orgB)
	insertUsage(orgA, leaseA1, 1000, 100, 200, 10, 0)
	insertUsage(orgA, leaseA2, 500, 50, 0, 0, 0)
	insertUsage(orgB, leaseB1, 500, 50, 0, 0, 0)
	oldEventAt := time.Now().UTC().Add(-40 * 24 * time.Hour)
	insertUsage(orgA, insertLease(orgA), 777, 77, 7, 7, 40*24*time.Hour)

	after, err := store.AdminOverview(ctx, 30)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}

	if got := after.Days; got != 30 {
		t.Fatalf("days=%d", got)
	}
	if after.UsageTrackingStart == nil {
		t.Fatal("usage_tracking_start is nil")
	}
	if before.UsageTrackingStart == nil && after.UsageTrackingStart.Sub(oldEventAt).Abs() > time.Second {
		t.Fatalf("usage_tracking_start=%v want ~%v", after.UsageTrackingStart, oldEventAt)
	}

	assertDelta := func(label string, got, want int64) {
		t.Helper()
		if got != want {
			t.Fatalf("%s delta=%d want %d", label, got, want)
		}
	}
	// orgs/users/active orgs each gained the two fixture organizations.
	assertDelta("orgs", after.Totals.Orgs-before.Totals.Orgs, 2)
	assertDelta("users", after.Totals.Users-before.Totals.Users, 2)
	assertDelta("active_orgs", after.Totals.ActiveOrgs-before.Totals.ActiveOrgs, 2)
	assertDelta("orders", after.Totals.Orders-before.Totals.Orders, 4)
	assertDelta("paid_orders", after.Totals.PaidOrders-before.Totals.PaidOrders, 2)
	assertDelta("revenue_cents", after.Totals.RevenueCents-before.Totals.RevenueCents, 15_000)

	// Product spend: relay includes both reason='usage' and reason='relay_usage';
	// the negative admin adjustment and the out-of-range row are excluded.
	assertDelta("relay spend", after.Totals.ProductSpendCredits["relay"]-before.Totals.ProductSpendCredits["relay"], 5_000_100)
	assertDelta("market spend", after.Totals.ProductSpendCredits["market"]-before.Totals.ProductSpendCredits["market"], 200)
	assertDelta("memory spend", after.Totals.ProductSpendCredits["causal-memory"]-before.Totals.ProductSpendCredits["causal-memory"], 50)
	assertDelta("dslpha spend", after.Totals.ProductSpendCredits["dslpha"]-before.Totals.ProductSpendCredits["dslpha"], 70)
	assertDelta("credits consumed", after.Totals.CreditsConsumed-before.Totals.CreditsConsumed, 5_000_420)

	assertDelta("tracked requests", after.Totals.Tokens.TrackedRequests-before.Totals.Tokens.TrackedRequests, 3)
	assertDelta("input tokens", after.Totals.Tokens.InputTokens-before.Totals.Tokens.InputTokens, 2000)
	assertDelta("output tokens", after.Totals.Tokens.OutputTokens-before.Totals.Tokens.OutputTokens, 200)
	assertDelta("cache read", after.Totals.Tokens.CacheReadTokens-before.Totals.Tokens.CacheReadTokens, 200)
	assertDelta("cache write", after.Totals.Tokens.CacheWriteTokens-before.Totals.Tokens.CacheWriteTokens, 10)
	assertDelta("total tokens", after.Totals.Tokens.TotalTokens-before.Totals.Tokens.TotalTokens, 2410)

	// Daily buckets are UTC and merge spend, tokens and revenue for the day.
	today := time.Now().UTC().Format("2006-01-02")
	bucket := func(rows []AdminOverviewDaily, date string) AdminOverviewDaily {
		for _, row := range rows {
			if row.Date == date {
				return row
			}
		}
		return AdminOverviewDaily{Date: date}
	}
	beforeDay := bucket(before.Daily, today)
	afterDay := bucket(after.Daily, today)
	assertDelta("daily relay", afterDay.Relay-beforeDay.Relay, 5_000_100)
	assertDelta("daily market", afterDay.Market-beforeDay.Market, 200)
	assertDelta("daily memory", afterDay.CausalMemory-beforeDay.CausalMemory, 50)
	assertDelta("daily dslpha", afterDay.Dslpha-beforeDay.Dslpha, 70)
	assertDelta("daily tokens", afterDay.Tokens-beforeDay.Tokens, 2410)
	// Only the two paid/fulfilled orders count toward daily revenue.
	assertDelta("daily revenue", afterDay.RevenueCents-beforeDay.RevenueCents, 15_000)
	for i := 1; i < len(after.Daily); i++ {
		if after.Daily[i-1].Date >= after.Daily[i].Date {
			t.Fatalf("daily is not oldest-first: %q then %q", after.Daily[i-1].Date, after.Daily[i].Date)
		}
	}

	// Top organizations rank by consumed credits; org A's fixture spend makes
	// it the top row. Per-org values are exact.
	if len(after.TopOrgs) == 0 || after.TopOrgs[0].OrgID != orgA {
		t.Fatalf("top orgs=%+v want %s first", after.TopOrgs, orgA)
	}
	topA := after.TopOrgs[0]
	if topA.Name != "Overview a" || topA.PlanCode != "pro" || topA.BalanceCredits != 5000 {
		t.Fatalf("top A identity=%+v", topA)
	}
	if topA.OwnerUserID != userA || topA.OwnerEmail != emailA || topA.TopModel != "deepseek-v4-pro" {
		t.Fatalf("top A owner/model=%+v", topA)
	}
	if topA.Orders != 3 || topA.RevenueCents != 15_000 {
		t.Fatalf("top A orders/revenue=%+v", topA)
	}
	if topA.RelayCredits != 5_000_100 || topA.MarketCredits != 200 ||
		topA.MemoryCredits != 50 || topA.DslphaCredits != 70 {
		t.Fatalf("top A spend=%+v", topA)
	}
	// Both in-range events belong to org A: 1000+500 input, 100+50 output,
	// 200 cache read and 10 cache write.
	if topA.Requests != 2 || topA.InputTokens != 1500 || topA.OutputTokens != 150 ||
		topA.CacheReadTokens != 200 || topA.CacheWriteTokens != 10 ||
		topA.TotalTokens != 1860 || topA.CacheHitRate != cacheHitRate(200, 1500) {
		t.Fatalf("top A tokens=%+v", topA)
	}
	var topB *AdminTopOrg
	for i := range after.TopOrgs {
		if after.TopOrgs[i].OrgID == orgB {
			topB = &after.TopOrgs[i]
			break
		}
	}
	if topB == nil {
		t.Fatalf("top orgs missing org B: %+v", after.TopOrgs)
	}
	if topB.Requests != 1 || topB.Orders != 1 || topB.RevenueCents != 0 ||
		topB.TotalTokens != 550 || topB.CacheHitRate != 0 ||
		topB.OwnerUserID != userB || topB.OwnerEmail != emailB ||
		topB.TopModel != "deepseek-v4-pro" {
		t.Fatalf("top B=%+v", topB)
	}

	// Model breakdown: the fixture model row is the only one with deltas.
	var model *AdminModelRow
	for i := range after.Models {
		if after.Models[i].Model == "deepseek-v4-pro" {
			model = &after.Models[i]
			break
		}
	}
	if model == nil {
		t.Fatalf("models missing deepseek-v4-pro: %+v", after.Models)
	}
	var beforeModel AdminModelRow
	for _, row := range before.Models {
		if row.Model == "deepseek-v4-pro" {
			beforeModel = row
			break
		}
	}
	if model.Requests-beforeModel.Requests != 3 ||
		model.InputTokens-beforeModel.InputTokens != 2000 ||
		model.OutputTokens-beforeModel.OutputTokens != 200 ||
		model.CacheReadTokens-beforeModel.CacheReadTokens != 200 ||
		model.CacheWriteTokens-beforeModel.CacheWriteTokens != 10 ||
		model.TotalTokens-beforeModel.TotalTokens != 2410 {
		t.Fatalf("model=%+v before=%+v", model, beforeModel)
	}

	// Users are one row per org owner, ordered by tokens. Other rows may exist
	// in a shared test database, so look the fixture orgs up by id.
	findUser := func(orgID string) *AdminUserRow {
		for i := range after.Users {
			if after.Users[i].OrgID == orgID {
				return &after.Users[i]
			}
		}
		return nil
	}
	userRowA := findUser(orgA)
	userRowB := findUser(orgB)
	if userRowA == nil || userRowB == nil {
		t.Fatalf("users missing fixture orgs: %+v", after.Users)
	}
	if userRowA.UserID != userA || userRowA.Email != emailA ||
		userRowA.TopModel != "deepseek-v4-pro" || userRowA.PlanCode != "pro" ||
		userRowA.Requests != 2 || userRowA.TotalTokens != 1860 ||
		userRowA.InputTokens != 1500 || userRowA.CacheReadTokens != 200 ||
		userRowA.CacheWriteTokens != 10 {
		t.Fatalf("user A=%+v", userRowA)
	}
	if userRowA.Orders != 3 || userRowA.RevenueCents != 15_000 ||
		userRowA.RelayCredits != 5_000_100 || userRowA.MarketCredits != 200 ||
		userRowA.MemoryCredits != 50 || userRowA.DslphaCredits != 70 {
		t.Fatalf("user A spend=%+v", userRowA)
	}
	if userRowB.UserID != userB || userRowB.Email != emailB ||
		userRowB.TotalTokens != 550 || userRowB.Orders != 1 {
		t.Fatalf("user B=%+v", userRowB)
	}
	for i := 1; i < len(after.Users); i++ {
		if after.Users[i-1].TotalTokens < after.Users[i].TotalTokens {
			t.Fatalf("users are not ordered by total tokens: %+v", after.Users)
		}
	}

	// Recent orders carry the joined organization name. Every fixture order is
	// in the newest page.
	gotOrders := map[string]bool{}
	for _, order := range after.RecentOrders {
		gotOrders[order.ID] = true
		if (order.ID == orderA1 || order.ID == orderA2 || order.ID == orderA3) && order.OrgName != "Overview a" {
			t.Fatalf("order %s org_name=%q", order.ID, order.OrgName)
		}
		if order.ID == orderB1 && order.OrgName != "Overview b" {
			t.Fatalf("order %s org_name=%q", order.ID, order.OrgName)
		}
		if order.ID == orderA1 && order.PaidAt == nil {
			t.Fatalf("paid order lost paid_at: %+v", order)
		}
	}
	for _, id := range []string{orderA1, orderA2, orderA3, orderB1} {
		if !gotOrders[id] {
			t.Fatalf("recent orders missing %s: %+v", id, after.RecentOrders)
		}
	}
}
