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

type usageCall struct {
	orgID string
	value int
}

type fakeUsageStore struct {
	summary    billing.UsageSummary
	summaryErr error
	daily      []billing.UsageDailyRow
	dailyErr   error
	recent     []billing.UsageRecentRow
	recentErr  error
	dayCalls   []usageCall
	limitCalls []usageCall
}

func (f *fakeUsageStore) UsageSummary(_ context.Context, orgID string, days int) (billing.UsageSummary, error) {
	f.dayCalls = append(f.dayCalls, usageCall{orgID: orgID, value: days})
	out := f.summary
	out.Days = days
	return out, f.summaryErr
}

func (f *fakeUsageStore) UsageDaily(_ context.Context, orgID string, days int) ([]billing.UsageDailyRow, error) {
	f.dayCalls = append(f.dayCalls, usageCall{orgID: orgID, value: days})
	return f.daily, f.dailyErr
}

func (f *fakeUsageStore) UsageRecent(_ context.Context, orgID string, limit int) ([]billing.UsageRecentRow, error) {
	f.limitCalls = append(f.limitCalls, usageCall{orgID: orgID, value: limit})
	return f.recent, f.recentErr
}

func usageTestHandler(t *testing.T, store usageAnalyticsStore) http.Handler {
	t.Helper()
	supabase := fakeSupabase(t)
	return (&Server{
		cfg:        testConfig(),
		verifier:   auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		membership: membershipFixture(),
		usage:      store,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
}

func TestUsageSummaryEndpointEmpty(t *testing.T) {
	store := &fakeUsageStore{}
	h := usageTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/summary?days=7", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body["days"] != float64(7) {
		t.Fatalf("days=%v", body["days"])
	}
	totals, ok := body["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals=%v", body["totals"])
	}
	for _, key := range []string{
		"requests", "input_tokens", "output_tokens", "cache_read_tokens",
		"cache_write_tokens", "total_tokens", "credits_minor", "upstream_cost_minor",
		"cache_hit_rate", "saved_tokens", "user_share_tokens", "user_share_minor",
		"summary_cost_minor",
	} {
		if totals[key] != float64(0) {
			t.Fatalf("totals.%s=%v want 0", key, totals[key])
		}
	}
	if len(store.dayCalls) != 1 || store.dayCalls[0] != (usageCall{orgID: "org-1", value: 7}) {
		t.Fatalf("store calls=%+v", store.dayCalls)
	}
}

func TestUsageSummaryEndpointTotals(t *testing.T) {
	store := &fakeUsageStore{summary: billing.UsageSummary{Totals: billing.UsageTotals{
		Requests: 12, InputTokens: 33404, OutputTokens: 256, CacheReadTokens: 8192,
		CacheWriteTokens: 0, TotalTokens: 41852, CreditsMinor: 6000,
		UpstreamCostMinor: 1616, CacheHitRate: 0.197, SavedTokens: 26026,
		UserShareTokens: 7807, UserShareMinor: 1542, SummaryCostMinor: 12,
	}}}
	h := usageTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/summary", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	totals, _ := body["totals"].(map[string]any)
	for key, want := range map[string]float64{
		"requests": 12, "input_tokens": 33404, "output_tokens": 256,
		"cache_read_tokens": 8192, "total_tokens": 41852, "credits_minor": 6000,
		"upstream_cost_minor": 1616, "saved_tokens": 26026,
		"user_share_tokens": 7807, "user_share_minor": 1542, "summary_cost_minor": 12,
	} {
		if totals[key] != want {
			t.Fatalf("totals.%s=%v want %v", key, totals[key], want)
		}
	}
	if totals["cache_hit_rate"] != 0.197 {
		t.Fatalf("cache_hit_rate=%v", totals["cache_hit_rate"])
	}
	// Absent days uses the documented default.
	if len(store.dayCalls) != 1 || store.dayCalls[0].value != billing.UsageDaysDefault {
		t.Fatalf("default days calls=%+v", store.dayCalls)
	}
}

func TestUsageDailyEndpoint(t *testing.T) {
	store := &fakeUsageStore{daily: []billing.UsageDailyRow{
		{Date: "2026-11-18", Requests: 2, InputTokens: 100, OutputTokens: 10, CacheReadTokens: 50,
			TotalTokens: 160, CreditsMinor: 2000, SavedTokens: 20, UserShareTokens: 6, UserShareMinor: 2},
		{Date: "2026-11-19", Requests: 1, InputTokens: 200, OutputTokens: 20, CacheReadTokens: 80,
			TotalTokens: 300, CreditsMinor: 3000, SavedTokens: 30, UserShareTokens: 9, UserShareMinor: 3},
	}}
	h := usageTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/daily?days=2", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, ok := body["days"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("days=%v", body["days"])
	}
	first, _ := rows[0].(map[string]any)
	second, _ := rows[1].(map[string]any)
	if first["date"] != "2026-11-18" || first["total_tokens"] != float64(160) ||
		first["credits_minor"] != float64(2000) || first["saved_tokens"] != float64(20) {
		t.Fatalf("first day=%v", first)
	}
	if second["date"] != "2026-11-19" || second["user_share_minor"] != float64(3) {
		t.Fatalf("second day=%v", second)
	}
	if len(store.dayCalls) != 1 || store.dayCalls[0] != (usageCall{orgID: "org-1", value: 2}) {
		t.Fatalf("store calls=%+v", store.dayCalls)
	}
}

func TestUsageDailyEndpointEmptyIsArray(t *testing.T) {
	h := usageTestHandler(t, &fakeUsageStore{daily: nil})
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/daily", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, ok := body["days"].([]any)
	if !ok || len(rows) != 0 {
		t.Fatalf("days=%v want empty array", body["days"])
	}
}

func TestUsageRecentEndpoint(t *testing.T) {
	created := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	store := &fakeUsageStore{recent: []billing.UsageRecentRow{{
		CreatedAt: created, Model: "deepseek-v4-pro", RequestID: "req-1", Requests: 1,
		InputTokens: 33404, OutputTokens: 256, CacheReadTokens: 8192, CacheWriteTokens: 0,
		TotalTokens: 41852, CacheHitRate: 0.197, CreditsMinor: 3000,
		SavedTokens: 26026, UserShareTokens: 7807, UserShareMinor: 1542,
		CompressionApplied: true, OriginalInputTokens: 33404, CompressedInputTokens: 7378,
	}}}
	h := usageTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/recent?limit=10", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, ok := body["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("rows=%v", body["rows"])
	}
	row, _ := rows[0].(map[string]any)
	if row["model"] != "deepseek-v4-pro" || row["request_id"] != "req-1" ||
		row["total_tokens"] != float64(41852) || row["credits_minor"] != float64(3000) ||
		row["original_input_tokens"] != float64(33404) || row["compressed_input_tokens"] != float64(7378) ||
		row["saved_tokens"] != float64(26026) || row["user_share_minor"] != float64(1542) {
		t.Fatalf("row=%v", row)
	}
	if body["limit"] != float64(10) {
		t.Fatalf("limit=%v", body["limit"])
	}
	if len(store.limitCalls) != 1 || store.limitCalls[0] != (usageCall{orgID: "org-1", value: 10}) {
		t.Fatalf("store calls=%+v", store.limitCalls)
	}
}

func TestUsageRecentEndpointEmptyIsArray(t *testing.T) {
	h := usageTestHandler(t, &fakeUsageStore{recent: nil})
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/recent", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, ok := body["rows"].([]any)
	if !ok || len(rows) != 0 {
		t.Fatalf("rows=%v want empty array", body["rows"])
	}
	if body["limit"] != float64(billing.UsageRecentDefaultLimit) {
		t.Fatalf("limit=%v", body["limit"])
	}
}

func TestUsageEndpointsCrossOrgForbidden(t *testing.T) {
	store := &fakeUsageStore{}
	h := usageTestHandler(t, store)
	paths := []string{"/usage/summary", "/usage/daily", "/usage/recent"}
	for _, path := range paths {
		rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-2"+path, "jwt-owner", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-org %s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		rec, _ = callJSON(t, h, http.MethodGet, "/v1/orgs/org-1"+path, "jwt-foreign", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("foreign %s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	if len(store.dayCalls) != 0 || len(store.limitCalls) != 0 {
		t.Fatalf("forbidden request reached the store: days=%+v limits=%+v", store.dayCalls, store.limitCalls)
	}
}

func TestUsageEndpointsAnonymous(t *testing.T) {
	store := &fakeUsageStore{}
	h := usageTestHandler(t, store)
	for _, path := range []string{"/usage/summary", "/usage/daily", "/usage/recent"} {
		rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1"+path, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	if len(store.dayCalls) != 0 || len(store.limitCalls) != 0 {
		t.Fatalf("anonymous request reached the store")
	}
}

func TestUsageQueryParamClampAndInvalid(t *testing.T) {
	store := &fakeUsageStore{}
	h := usageTestHandler(t, store)

	// days: absent/empty uses 30, negative and zero clamp to 1, huge clamps to 365.
	for raw, want := range map[string]int{
		"": 30, "0": 1, "-7": 1, "1": 1, "30": 30, "365": 365, "9999": 365,
	} {
		path := "/v1/orgs/org-1/usage/summary"
		if raw != "" {
			path += "?days=" + raw
		}
		rec, _ := callJSON(t, h, http.MethodGet, path, "jwt-owner", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("days=%q status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
		last := store.dayCalls[len(store.dayCalls)-1]
		if last != (usageCall{orgID: "org-1", value: want}) {
			t.Fatalf("days=%q store call=%+v want=%d", raw, last, want)
		}
	}
	for _, raw := range []string{"abc", "1.5", "1d"} {
		rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/summary?days="+raw, "jwt-owner", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("days=%q status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
		errBody, _ := body["error"].(map[string]any)
		if errBody["code"] != "invalid_days" {
			t.Fatalf("days=%q error=%v", raw, body)
		}
	}
	before := len(store.dayCalls)

	// limit: absent uses 50, zero/negative clamp to 1, huge clamps to 200.
	for raw, want := range map[string]int{
		"": 50, "0": 1, "-7": 1, "1": 1, "50": 50, "200": 200, "9999": 200,
	} {
		path := "/v1/orgs/org-1/usage/recent"
		if raw != "" {
			path += "?limit=" + raw
		}
		rec, _ := callJSON(t, h, http.MethodGet, path, "jwt-owner", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("limit=%q status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
		last := store.limitCalls[len(store.limitCalls)-1]
		if last != (usageCall{orgID: "org-1", value: want}) {
			t.Fatalf("limit=%q store call=%+v want=%d", raw, last, want)
		}
	}
	for _, raw := range []string{"abc", "1.5", "1d"} {
		rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/recent?limit="+raw, "jwt-owner", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
		errBody, _ := body["error"].(map[string]any)
		if errBody["code"] != "invalid_limit" {
			t.Fatalf("limit=%q error=%v", raw, body)
		}
	}
	// Invalid params never reach the store; only the valid summary calls above did.
	if len(store.dayCalls) != before {
		t.Fatalf("invalid days reached the store: %+v", store.dayCalls)
	}
}

func TestUsageRouteWithoutStoreReportsUnavailable(t *testing.T) {
	// A server with no usage seam falls back to its billing store, which is nil
	// in this test. This guards the production wiring path.
	h := usageTestHandler(t, nil)
	rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/usage/summary", "jwt-owner", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
