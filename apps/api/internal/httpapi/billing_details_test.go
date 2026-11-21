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

type fakeBillingDetailsStore struct {
	out   billing.BillingDetails
	err   error
	calls []usageCall
}

func (f *fakeBillingDetailsStore) BillingDetails(_ context.Context, orgID string, days int) (billing.BillingDetails, error) {
	f.calls = append(f.calls, usageCall{orgID: orgID, value: days})
	out := f.out
	out.Days = days
	return out, f.err
}

func billingDetailsTestHandler(t *testing.T, store billingDetailsStore) http.Handler {
	t.Helper()
	supabase := fakeSupabase(t)
	return (&Server{
		cfg:            testConfig(),
		verifier:       auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		membership:     membershipFixture(),
		billingDetails: store,
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
}

func TestBillingDetailsEndpointShape(t *testing.T) {
	creditsMinor := int64(3000)
	credits := int64(3)
	yuan := 0.03
	started := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	ended := started.Add(2 * time.Hour)
	store := &fakeBillingDetailsStore{out: billing.BillingDetails{
		RangeStart: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
		RangeEnd:   time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
		Summary: billing.BillingDetailsSummary{
			TotalCredits: 7, TotalMinor: 7000, TotalYuan: 0.07,
			TokenCredits: 3, TokenMinor: 3000,
			SandboxCredits: 4, SandboxMinor: 4000, SandboxCount: 1,
			Requests: 2, TotalTokens: 1500,
		},
		Tokens: []billing.BillingTokenRow{{
			Model: "deepseek-v4-pro", Requests: 2, InputTokens: 1000, OutputTokens: 500,
			CacheReadTokens: 0, CacheWriteTokens: 0, TotalTokens: 1500,
			CreditsMinor: &creditsMinor, Credits: &credits, Yuan: &yuan,
		}},
		Sandboxes: []billing.BillingSandboxRow{{
			ID: 9, SandboxID: "sbx-1", Provider: "daytona", ProviderSandboxID: "daytona-1",
			UserID: "user-1", Product: "market", StartedAt: &started, EndedAt: &ended,
			BillableSeconds: 7200, BillableHours: 2, CPUSeconds: 7200,
			MemoryMiBSeconds: 7200, DiskMiBSeconds: 7200,
			CPUCredits: 1.5, MemoryCredits: 1.5, DiskCredits: 1, GPUCredits: 0,
			TotalCredits: 4, CreditsMinor: 4000, Yuan: 0.04,
			UnitPrices: []byte(`{"cpu_credits_per_second":0.0002}`),
		}},
	}}
	h := billingDetailsTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/details?days=30", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body["days"] != float64(30) || body["range_start"] != "2026-10-22T00:00:00Z" {
		t.Fatalf("range=%v %v", body["days"], body["range_start"])
	}
	summary, _ := body["summary"].(map[string]any)
	for key, want := range map[string]float64{
		"total_credits": 7, "total_minor": 7000, "total_yuan": 0.07,
		"token_credits": 3, "token_minor": 3000,
		"sandbox_credits": 4, "sandbox_minor": 4000, "sandbox_count": 1,
		"requests": 2, "total_tokens": 1500,
	} {
		if summary[key] != want {
			t.Fatalf("summary.%s=%v want %v", key, summary[key], want)
		}
	}
	tokens, _ := body["tokens"].([]any)
	if len(tokens) != 1 {
		t.Fatalf("tokens=%v", body["tokens"])
	}
	tokenRow, _ := tokens[0].(map[string]any)
	if tokenRow["model"] != "deepseek-v4-pro" || tokenRow["credits_minor"] != float64(3000) ||
		tokenRow["total_tokens"] != float64(1500) || tokenRow["yuan"] != 0.03 {
		t.Fatalf("token row=%v", tokenRow)
	}
	// Undefined per-class effective rates are explicitly null, never invented.
	if v, ok := tokenRow["effective_input_credits_per_million"]; !ok || v != nil {
		t.Fatalf("effective input=%v", tokenRow["effective_input_credits_per_million"])
	}
	sandboxes, _ := body["sandboxes"].([]any)
	if len(sandboxes) != 1 {
		t.Fatalf("sandboxes=%v", body["sandboxes"])
	}
	sandboxRow, _ := sandboxes[0].(map[string]any)
	if sandboxRow["id"] != float64(9) || sandboxRow["sandbox_id"] != "sbx-1" ||
		sandboxRow["billable_hours"] != float64(2) || sandboxRow["credits_minor"] != float64(4000) ||
		sandboxRow["yuan"] != 0.04 {
		t.Fatalf("sandbox row=%v", sandboxRow)
	}
	prices, _ := sandboxRow["unit_prices"].(map[string]any)
	if prices["cpu_credits_per_second"] != 0.0002 {
		t.Fatalf("unit prices=%v", sandboxRow["unit_prices"])
	}
	if len(store.calls) != 1 || store.calls[0] != (usageCall{orgID: "org-1", value: 30}) {
		t.Fatalf("store calls=%+v", store.calls)
	}
}

func TestBillingDetailsEmptyArrays(t *testing.T) {
	h := billingDetailsTestHandler(t, &fakeBillingDetailsStore{})
	rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/details", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rows, ok := body["tokens"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("tokens=%v want []", body["tokens"])
	}
	if rows, ok := body["sandboxes"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("sandboxes=%v want []", body["sandboxes"])
	}
}

func TestBillingDetailsDaysClampAndInvalid(t *testing.T) {
	store := &fakeBillingDetailsStore{}
	h := billingDetailsTestHandler(t, store)
	for raw, want := range map[string]int{
		"": 30, "0": 1, "-7": 1, "1": 1, "90": 90, "365": 365, "9999": 365,
	} {
		path := "/v1/orgs/org-1/billing/details"
		if raw != "" {
			path += "?days=" + raw
		}
		rec, _ := callJSON(t, h, http.MethodGet, path, "jwt-owner", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("days=%q status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
		last := store.calls[len(store.calls)-1]
		if last != (usageCall{orgID: "org-1", value: want}) {
			t.Fatalf("days=%q call=%+v want=%d", raw, last, want)
		}
	}
	before := len(store.calls)
	for _, raw := range []string{"abc", "1.5", "1d"} {
		rec, body := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/details?days="+raw, "jwt-owner", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("days=%q status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
		errBody, _ := body["error"].(map[string]any)
		if errBody["code"] != "invalid_days" {
			t.Fatalf("days=%q body=%v", raw, body)
		}
	}
	if len(store.calls) != before {
		t.Fatalf("invalid days reached the store: %+v", store.calls)
	}
}

func TestBillingDetailsAuth(t *testing.T) {
	store := &fakeBillingDetailsStore{}
	h := billingDetailsTestHandler(t, store)
	if rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/details", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", rec.Code)
	}
	if rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-2/billing/details", "jwt-owner", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-org status=%d", rec.Code)
	}
	if rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/details", "jwt-foreign", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign status=%d", rec.Code)
	}
	if len(store.calls) != 0 {
		t.Fatalf("forbidden request reached the store: %+v", store.calls)
	}
}

func TestBillingDetailsRouteWithoutStoreReportsUnavailable(t *testing.T) {
	h := billingDetailsTestHandler(t, nil)
	rec, _ := callJSON(t, h, http.MethodGet, "/v1/orgs/org-1/billing/details", "jwt-owner", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
