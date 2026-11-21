package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/auth"
	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

type compressionCall struct {
	orgID string
	limit int
}

type fakeCompressionStore struct {
	usage billing.CompressionUsage
	err   error
	calls []compressionCall
}

func (f *fakeCompressionStore) CompressionUsage(_ context.Context, orgID string, limit int) (billing.CompressionUsage, error) {
	f.calls = append(f.calls, compressionCall{orgID: orgID, limit: limit})
	usage := f.usage
	usage.Limit = limit
	return usage, f.err
}

func compressionTestHandler(t *testing.T, store compressionUsageStore) http.Handler {
	t.Helper()
	supabase := fakeSupabase(t)
	return (&Server{
		cfg:         testConfig(),
		verifier:    auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		membership:  membershipFixture(),
		compression: store,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
}

func TestCompressionUsageEndpointEmpty(t *testing.T) {
	store := &fakeCompressionStore{}
	h := compressionTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/compression?limit=20", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, ok := body["rows"].([]any)
	if !ok || len(rows) != 0 {
		t.Fatalf("rows=%v want empty array", body["rows"])
	}
	totals, ok := body["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals=%v", body["totals"])
	}
	for _, key := range []string{"count", "saved_tokens", "user_share_tokens", "user_share_minor", "summary_cost_minor"} {
		if totals[key] != float64(0) {
			t.Fatalf("totals.%s=%v want 0", key, totals[key])
		}
	}
	if len(store.calls) != 1 || store.calls[0] != (compressionCall{orgID: "org-1", limit: 20}) {
		t.Fatalf("store calls=%+v", store.calls)
	}
}

func TestCompressionUsageEndpointTotalsAndRows(t *testing.T) {
	created := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	store := &fakeCompressionStore{usage: billing.CompressionUsage{
		Rows: []billing.CompressionRow{
			{
				ID: 2, OrgID: "org-1", RequestID: "req-2", SettlementID: "stl-2", LeaseID: "lease-2",
				Applied: true, OriginalInputTokens: 1000, CompressedInputTokens: 400,
				SavedTokens: 600, UserShareTokens: 180, UserShareMinor: 50, SummaryCostMinor: 10,
				CreatedAt: created,
			},
			{
				ID: 1, OrgID: "org-1", RequestID: "req-1", SettlementID: "stl-1", LeaseID: "lease-1",
				Applied: true, OriginalInputTokens: 33404, CompressedInputTokens: 7378,
				SavedTokens: 26026, UserShareTokens: 7807, UserShareMinor: 1542, SummaryCostMinor: 0,
				CreatedAt: created.Add(-time.Hour),
			},
		},
		Totals: billing.CompressionTotals{
			Count: 2, SavedTokens: 26626, UserShareTokens: 7987,
			UserShareMinor: 1592, SummaryCostMinor: 10,
		},
	}}
	h := compressionTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/compression?limit=20", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, _ := body["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows=%v", body["rows"])
	}
	first, _ := rows[0].(map[string]any)
	if first["settlement_id"] != "stl-2" || first["saved_tokens"] != float64(600) {
		t.Fatalf("newest row=%v", first)
	}
	totals, _ := body["totals"].(map[string]any)
	if totals["count"] != float64(2) || totals["saved_tokens"] != float64(26626) ||
		totals["user_share_tokens"] != float64(7987) || totals["user_share_minor"] != float64(1592) ||
		totals["summary_cost_minor"] != float64(10) {
		t.Fatalf("totals=%v", totals)
	}
	if body["limit"] != float64(20) {
		t.Fatalf("limit=%v", body["limit"])
	}
}

func TestCompressionUsageEndpointCrossOrgForbidden(t *testing.T) {
	store := &fakeCompressionStore{}
	h := compressionTestHandler(t, store)

	// user-owner belongs to org-1, not org-2: reject before the store is read.
	rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-2/billing/compression", "jwt-owner", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-org status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("cross-org request reached the store: %+v", store.calls)
	}

	rec, _ = callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/compression", "jwt-foreign", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("foreign request reached the store: %+v", store.calls)
	}
}

func TestCompressionUsageEndpointAnonymous(t *testing.T) {
	store := &fakeCompressionStore{}
	h := compressionTestHandler(t, store)
	rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/compression", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("anonymous request reached the store: %+v", store.calls)
	}
}

func TestCompressionUsageEndpointLimit(t *testing.T) {
	store := &fakeCompressionStore{}
	h := compressionTestHandler(t, store)

	for _, limit := range []string{"0", "-1", "abc", "1.5"} {
		rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/compression?limit="+limit, "jwt-owner", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q status=%d body=%s", limit, rec.Code, rec.Body.String())
		}
		errBody, _ := body["error"].(map[string]any)
		if errBody["code"] != "invalid_limit" {
			t.Fatalf("limit=%q error=%v", limit, body)
		}
	}
	if len(store.calls) != 0 {
		t.Fatalf("invalid limits reached the store: %+v", store.calls)
	}

	// Absent limit uses the store default; an oversized one is clamped.
	callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/compression", "jwt-owner", "")
	callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/compression?limit=9999", "jwt-owner", "")
	want := []compressionCall{
		{orgID: "org-1", limit: billing.CompressionDefaultLimit},
		{orgID: "org-1", limit: billing.CompressionMaxLimit},
	}
	if len(store.calls) != 2 || store.calls[0] != want[0] || store.calls[1] != want[1] {
		t.Fatalf("store calls=%+v want=%+v", store.calls, want)
	}
}

func TestCompressionUsageRouteConfiguredReportsEmpty(t *testing.T) {
	// A server with no compression seam falls back to its billing store. This
	// guards the production wiring path without needing Postgres.
	h := compressionTestHandler(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example/v1/orgs/org-1/billing/compression", nil)
	req.Header.Set("Authorization", "Bearer jwt-owner")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
