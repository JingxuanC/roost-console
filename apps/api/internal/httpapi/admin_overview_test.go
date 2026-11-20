package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/auth"
	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

type fakeAdminOverviewStore struct {
	out   billing.AdminOverview
	err   error
	calls []int
}

func (f *fakeAdminOverviewStore) AdminOverview(_ context.Context, days int) (billing.AdminOverview, error) {
	f.calls = append(f.calls, days)
	out := f.out
	out.Days = days
	return out, f.err
}

func adminOverviewTestHandler(t *testing.T, store adminOverviewStore) http.Handler {
	t.Helper()
	supabase := fakeSupabase(t)
	cfg := testConfig()
	cfg.AdminEmails = []string{"owner@example.com"}
	return (&Server{
		cfg:        cfg,
		verifier:   auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		membership: membershipFixture(),
		overview:   store,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
}

func adminOverviewFixture() billing.AdminOverview {
	now := time.Now().UTC()
	start := now.Add(-48 * time.Hour)
	paid := now.Add(-24 * time.Hour)
	return billing.AdminOverview{
		UsageTrackingStart: &start,
		Totals: billing.AdminOverviewTotals{
			Orgs: 15, Users: 20, ActiveOrgs: 6,
			Orders: 4, PaidOrders: 3, RevenueCents: 25000, CreditsConsumed: 1937,
			Tokens: billing.AdminOverviewTokens{
				TrackedRequests: 5,
				InputTokens:     1000, OutputTokens: 200, CacheReadTokens: 400,
				CacheWriteTokens: 50, TotalTokens: 1650, CacheHitRate: 0.2857,
			},
			ProductSpendCredits: map[string]int64{
				"relay": 1300, "market": 20, "causal-memory": 29, "dslpha": 588,
			},
		},
		Daily: []billing.AdminOverviewDaily{{
			Date: "2026-11-20", Relay: 1300, Market: 20, CausalMemory: 29,
			Dslpha: 588, Tokens: 1650, RevenueCents: 10000,
		}},
		Models: []billing.AdminModelRow{{
			Model: "deepseek-v4-pro", Requests: 5, InputTokens: 1000,
			OutputTokens: 200, CacheReadTokens: 400, CacheWriteTokens: 50,
			TotalTokens: 1650, CacheHitRate: 0.2857,
		}},
		TopOrgs: []billing.AdminTopOrg{{
			OrgID: "org-1", Name: "Alpha", OwnerUserID: "user-1",
			OwnerEmail: "owner@example.com", PlanCode: "pro", BalanceCredits: 5000,
			Orders: 2, RevenueCents: 15000, RelayCredits: 1300, MarketCredits: 20,
			MemoryCredits: 29, DslphaCredits: 588, Requests: 5, InputTokens: 1000,
			OutputTokens: 200, CacheReadTokens: 400, CacheWriteTokens: 50,
			TotalTokens: 1650, CacheHitRate: 0.2857, TopModel: "deepseek-v4-pro",
		}},
		Users: []billing.AdminUserRow{{
			OrgID: "org-1", OrgName: "Alpha", UserID: "user-1",
			Email: "owner@example.com", PlanCode: "pro", BalanceCredits: 5000,
			Orders: 2, RevenueCents: 15000, RelayCredits: 1300, MarketCredits: 20,
			MemoryCredits: 29, DslphaCredits: 588, Requests: 5, InputTokens: 1000,
			OutputTokens: 200, CacheReadTokens: 400, CacheWriteTokens: 50,
			TotalTokens: 1650, CacheHitRate: 0.2857, TopModel: "deepseek-v4-pro",
		}},
		RecentOrders: []billing.AdminRecentOrder{{
			ID: "order-1", OrgID: "org-1", OrgName: "Alpha", SKU: "credit_100",
			AmountCents: 10000, Credits: 10000, Status: "paid",
			CreatedAt: now, PaidAt: &paid,
		}},
	}
}

func TestAdminOverviewAdminGate(t *testing.T) {
	store := &fakeAdminOverviewStore{out: adminOverviewFixture()}
	h := adminOverviewTestHandler(t, store)

	// A signed-in non-admin is forbidden by the same ADMIN_EMAILS gate as the
	// organization tools.
	rec, body := callJSON(t, h, http.MethodGet, "/v1/admin/overview", "jwt-other", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status=%d body=%v", rec.Code, body)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "forbidden" {
		t.Fatalf("non-admin error=%v", body)
	}

	// Missing credentials fail authentication before the allowlist.
	rec, _ = callJSON(t, h, http.MethodGet, "/v1/admin/overview", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", rec.Code)
	}
	if len(store.calls) != 0 {
		t.Fatalf("denied requests reached the store: %v", store.calls)
	}

	rec, _ = callJSON(t, h, http.MethodGet, "/v1/admin/overview", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 1 || store.calls[0] != billing.AdminOverviewDaysDefault {
		t.Fatalf("admin calls=%v", store.calls)
	}
}

func TestAdminOverviewResponseShape(t *testing.T) {
	store := &fakeAdminOverviewStore{out: adminOverviewFixture()}
	h := adminOverviewTestHandler(t, store)

	rec, body := callJSON(t, h, http.MethodGet, "/v1/admin/overview?days=7", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body["days"] != float64(7) {
		t.Fatalf("days=%v", body["days"])
	}
	if _, ok := body["usage_tracking_start"].(string); !ok {
		t.Fatalf("usage_tracking_start=%v", body["usage_tracking_start"])
	}
	totals, ok := body["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals=%v", body["totals"])
	}
	for _, key := range []string{
		"orgs", "users", "active_orgs", "orders", "paid_orders", "revenue_cents",
		"credits_consumed", "tokens", "product_spend_credits",
	} {
		if _, ok := totals[key]; !ok {
			t.Fatalf("totals missing %s: %v", key, totals)
		}
	}
	tokens, _ := totals["tokens"].(map[string]any)
	for _, key := range []string{"tracked_requests", "input", "output", "cache_read", "cache_write", "total", "cache_hit_rate"} {
		if _, ok := tokens[key]; !ok {
			t.Fatalf("totals.tokens missing %s: %v", key, tokens)
		}
	}
	if tokens["tracked_requests"] != float64(5) {
		t.Fatalf("tracked_requests=%v want 5", tokens["tracked_requests"])
	}
	spend, _ := totals["product_spend_credits"].(map[string]any)
	if spend["relay"] != float64(1300) || spend["causal-memory"] != float64(29) {
		t.Fatalf("product_spend_credits=%v", spend)
	}

	daily, _ := body["daily"].([]any)
	if len(daily) != 1 {
		t.Fatalf("daily=%v", body["daily"])
	}
	day, _ := daily[0].(map[string]any)
	for _, key := range []string{"date", "relay", "market", "causal-memory", "dslpha", "tokens", "revenue_cents"} {
		if _, ok := day[key]; !ok {
			t.Fatalf("daily missing %s: %v", key, day)
		}
	}

	models, _ := body["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models=%v", body["models"])
	}
	model, _ := models[0].(map[string]any)
	for _, key := range []string{
		"model", "requests", "input_tokens", "output_tokens", "cache_read_tokens",
		"cache_write_tokens", "total_tokens", "cache_hit_rate",
	} {
		if _, ok := model[key]; !ok {
			t.Fatalf("models missing %s: %v", key, model)
		}
	}

	top, _ := body["top_orgs"].([]any)
	if len(top) != 1 {
		t.Fatalf("top_orgs=%v", body["top_orgs"])
	}
	org, _ := top[0].(map[string]any)
	for _, key := range []string{
		"org_id", "name", "owner_user_id", "owner_email", "plan_code",
		"balance_credits", "orders", "revenue_cents", "relay_credits",
		"market_credits", "memory_credits", "dslpha_credits", "requests",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
		"total_tokens", "cache_hit_rate", "top_model",
	} {
		if _, ok := org[key]; !ok {
			t.Fatalf("top_orgs missing %s: %v", key, org)
		}
	}
	if org["owner_email"] != "owner@example.com" || org["top_model"] != "deepseek-v4-pro" {
		t.Fatalf("top_orgs identity=%v/%v", org["owner_email"], org["top_model"])
	}

	users, _ := body["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("users=%v", body["users"])
	}
	user, _ := users[0].(map[string]any)
	for _, key := range []string{
		"org_id", "org_name", "user_id", "email", "plan_code",
		"balance_credits", "orders", "revenue_cents", "relay_credits",
		"market_credits", "memory_credits", "dslpha_credits", "requests",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
		"total_tokens", "cache_hit_rate", "top_model",
	} {
		if _, ok := user[key]; !ok {
			t.Fatalf("users missing %s: %v", key, user)
		}
	}
	if user["email"] != "owner@example.com" || user["total_tokens"] != float64(1650) {
		t.Fatalf("users row=%v", user)
	}

	orders, _ := body["recent_orders"].([]any)
	if len(orders) != 1 {
		t.Fatalf("recent_orders=%v", body["recent_orders"])
	}
	order, _ := orders[0].(map[string]any)
	for _, key := range []string{"id", "org_id", "org_name", "sku", "amount_cents", "credits", "status", "created_at", "paid_at"} {
		if _, ok := order[key]; !ok {
			t.Fatalf("recent_orders missing %s: %v", key, order)
		}
	}
}

func TestAdminOverviewEmpty(t *testing.T) {
	store := &fakeAdminOverviewStore{out: billing.AdminOverview{
		Totals:       billing.AdminOverviewTotals{ProductSpendCredits: map[string]int64{}},
		Daily:        []billing.AdminOverviewDaily{},
		Models:       []billing.AdminModelRow{},
		TopOrgs:      []billing.AdminTopOrg{},
		Users:        []billing.AdminUserRow{},
		RecentOrders: []billing.AdminRecentOrder{},
	}}
	h := adminOverviewTestHandler(t, store)

	rec, body := callJSON(t, h, http.MethodGet, "/v1/admin/overview", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body["usage_tracking_start"] != nil {
		t.Fatalf("usage_tracking_start=%v want null", body["usage_tracking_start"])
	}
	if daily, ok := body["daily"].([]any); !ok || len(daily) != 0 {
		t.Fatalf("daily=%v want []", body["daily"])
	}
	if models, ok := body["models"].([]any); !ok || len(models) != 0 {
		t.Fatalf("models=%v want []", body["models"])
	}
	if top, ok := body["top_orgs"].([]any); !ok || len(top) != 0 {
		t.Fatalf("top_orgs=%v want []", body["top_orgs"])
	}
	if users, ok := body["users"].([]any); !ok || len(users) != 0 {
		t.Fatalf("users=%v want []", body["users"])
	}
	if orders, ok := body["recent_orders"].([]any); !ok || len(orders) != 0 {
		t.Fatalf("recent_orders=%v want []", body["recent_orders"])
	}
}

func TestAdminOverviewDaysParameter(t *testing.T) {
	store := &fakeAdminOverviewStore{out: adminOverviewFixture()}
	h := adminOverviewTestHandler(t, store)

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"?days=7", 7},
		{"?days=90", 90},
		{"?days=0", 1},
		{"?days=-5", 1},
		{"?days=500", billing.AdminOverviewDaysMax},
		{"", billing.AdminOverviewDaysDefault},
	} {
		store.calls = nil
		rec, body := callJSON(t, h, http.MethodGet, "/v1/admin/overview"+tc.query, "jwt-owner", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%q status=%d body=%s", tc.query, rec.Code, rec.Body.String())
		}
		if body["days"] != float64(tc.want) {
			t.Fatalf("%q days=%v want %d", tc.query, body["days"], tc.want)
		}
		if len(store.calls) != 1 || store.calls[0] != tc.want {
			t.Fatalf("%q store calls=%v want %d", tc.query, store.calls, tc.want)
		}
	}

	store.calls = nil
	rec, body := callJSON(t, h, http.MethodGet, "/v1/admin/overview?days=abc", "jwt-owner", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid days status=%d body=%v", rec.Code, body)
	}
	if len(store.calls) != 0 {
		t.Fatalf("invalid days reached the store: %v", store.calls)
	}
}

func TestAdminOverviewStoreUnavailable(t *testing.T) {
	supabase := fakeSupabase(t)
	cfg := testConfig()
	cfg.AdminEmails = []string{"owner@example.com"}
	h := (&Server{
		cfg:      cfg,
		verifier: auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()

	rec, body := callJSON(t, h, http.MethodGet, "/v1/admin/overview", "jwt-owner", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
}
