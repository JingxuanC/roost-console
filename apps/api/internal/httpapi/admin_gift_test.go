package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/auth"
	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

type giftCall struct {
	orgID    string
	credits  int64
	note     string
	key      string
	operator string
}

type fakeAdminGiftStore struct {
	balance int64
	err     error
	calls   []giftCall
}

func (f *fakeAdminGiftStore) AdminGiftCredits(_ context.Context, orgID string, credits int64, note, key, operator string) (int64, error) {
	f.calls = append(f.calls, giftCall{orgID: orgID, credits: credits, note: note, key: key, operator: operator})
	return f.balance, f.err
}

type fakeAdminReferralStore struct {
	out    billing.AdminReferrals
	err    error
	limits []int
}

func (f *fakeAdminReferralStore) AdminReferrals(_ context.Context, limit int) (billing.AdminReferrals, error) {
	f.limits = append(f.limits, limit)
	return f.out, f.err
}

func adminGiftTestHandler(t *testing.T, gift adminGiftBilling, referrals adminReferralBilling) http.Handler {
	t.Helper()
	supabase := fakeSupabase(t)
	cfg := testConfig()
	cfg.AdminEmails = []string{"owner@example.com"}
	return (&Server{
		cfg:           cfg,
		verifier:      auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		membership:    membershipFixture(),
		adminGift:     gift,
		adminReferral: referrals,
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
}

func TestAdminGiftCreditsSuccessHeaderKey(t *testing.T) {
	store := &fakeAdminGiftStore{balance: 2500}
	h := adminGiftTestHandler(t, store, nil)
	rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orgs/org-1/gift", "jwt-owner",
		`{"credits":500,"note":"VIP 补偿"}`, map[string]string{"Idempotency-Key": "gift-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["ok"] != true || body["balance_credits"] != float64(2500) || body["credits"] != float64(500) {
		t.Fatalf("body=%v", body)
	}
	if len(store.calls) != 1 {
		t.Fatalf("calls=%v", store.calls)
	}
	call := store.calls[0]
	if call.orgID != "org-1" || call.credits != 500 || call.note != "VIP 补偿" ||
		call.key != "gift-1" || call.operator != "owner@example.com" {
		t.Fatalf("call=%+v", call)
	}
}

func TestAdminGiftCreditsSuccessBodyKey(t *testing.T) {
	store := &fakeAdminGiftStore{balance: 1500}
	h := adminGiftTestHandler(t, store, nil)
	rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orgs/org-1/gift", "jwt-owner",
		`{"credits":500,"note":"QA","idempotency_key":"gift-body-1"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if len(store.calls) != 1 || store.calls[0].key != "gift-body-1" {
		t.Fatalf("calls=%v", store.calls)
	}
}

func TestAdminGiftCreditsAdminOnly(t *testing.T) {
	store := &fakeAdminGiftStore{}
	h := adminGiftTestHandler(t, store, nil)
	// A signed-in non-admin and an anonymous caller are both refused before the
	// store is touched.
	rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orgs/org-1/gift", "jwt-other",
		`{"credits":100,"idempotency_key":"k1"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status=%d body=%v", rec.Code, body)
	}
	if rec, _ := callJSONBody(t, h, http.MethodPost, "/v1/admin/orgs/org-1/gift", "",
		`{"credits":100,"idempotency_key":"k1"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", rec.Code)
	}
	if len(store.calls) != 0 {
		t.Fatalf("denied gifts reached the store: %v", store.calls)
	}
}

func TestAdminGiftCreditsValidation(t *testing.T) {
	store := &fakeAdminGiftStore{}
	h := adminGiftTestHandler(t, store, nil)
	cases := []struct {
		name string
		body string
		key  string
	}{
		{"zero-credits", `{"credits":0,"idempotency_key":"k1"}`, ""},
		{"negative-credits", `{"credits":-5,"idempotency_key":"k1"}`, ""},
		{"missing-key", `{"credits":100}`, ""},
		{"invalid-json", `{`, ""},
		{"long-note", `{"credits":100,"note":"` + strings.Repeat("n", 501) + `","idempotency_key":"k1"}`, ""},
	}
	for _, tc := range cases {
		rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orgs/org-1/gift", "jwt-owner", tc.body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%v", tc.name, rec.Code, body)
		}
	}
	if len(store.calls) != 0 {
		t.Fatalf("invalid gifts reached the store: %v", store.calls)
	}
}

func TestAdminGiftCreditsErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not-found", billing.ErrNotFound, http.StatusNotFound},
		{"invalid", billing.ErrInvalidGiftCredits, http.StatusBadRequest},
		{"conflict", billing.ErrGiftIdempotencyConflict, http.StatusConflict},
		{"unexpected", errors.New("db down"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAdminGiftStore{err: tc.err}
			h := adminGiftTestHandler(t, store, nil)
			rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orgs/org-1/gift", "jwt-owner",
				`{"credits":100,"idempotency_key":"k1"}`, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%v", rec.Code, tc.want, body)
			}
		})
	}
}

func TestAdminGiftCreditsStoreUnavailable(t *testing.T) {
	h := adminGiftTestHandler(t, nil, nil)
	rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orgs/org-1/gift", "jwt-owner",
		`{"credits":100,"idempotency_key":"k1"}`, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
}

func TestAdminReferralsEndpoint(t *testing.T) {
	created := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	rewarded := created.Add(time.Minute)
	store := &fakeAdminReferralStore{out: billing.AdminReferrals{
		TotalReferrals: 3, RewardedReferrals: 2,
		TotalInviterCredits: 2000, TotalInviteeCredits: 2000,
		Referrals: []billing.AdminReferralRow{{
			ID: "ref-1", Code: "ABCD2345",
			InviterUserID: "user-owner", InviterOrgID: "org-1", InviterOrgName: "Alpha",
			InviteeUserID: "user-other", InviteeOrgID: "org-2", InviteeOrgName: "Beta",
			Status: "rewarded", InviterCredits: 1000, InviteeCredits: 1000,
			CreatedAt: created, RewardedAt: &rewarded,
		}},
	}}
	h := adminGiftTestHandler(t, nil, store)

	rec, body := callJSON(t, h, http.MethodGet, "/v1/admin/referrals", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["total_referrals"] != float64(3) || body["rewarded_referrals"] != float64(2) ||
		body["total_inviter_credits"] != float64(2000) {
		t.Fatalf("summary=%v", body)
	}
	rows, _ := body["referrals"].([]any)
	if len(rows) != 1 {
		t.Fatalf("referrals=%v", body["referrals"])
	}
	row, _ := rows[0].(map[string]any)
	if row["code"] != "ABCD2345" || row["inviter_org_name"] != "Alpha" ||
		row["invitee_org_name"] != "Beta" || row["status"] != "rewarded" {
		t.Fatalf("row=%v", row)
	}
	if len(store.limits) != 1 || store.limits[0] != 50 {
		t.Fatalf("limits=%v", store.limits)
	}

	rec, _ = callJSON(t, h, http.MethodGet, "/v1/admin/referrals?limit=999", "jwt-owner", "")
	if rec.Code != http.StatusOK || store.limits[len(store.limits)-1] != 200 {
		t.Fatalf("clamped status=%d limits=%v", rec.Code, store.limits)
	}
	rec, body = callJSON(t, h, http.MethodGet, "/v1/admin/referrals?limit=abc", "jwt-owner", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d body=%v", rec.Code, body)
	}
	if rec, _ := callJSON(t, h, http.MethodGet, "/v1/admin/referrals", "jwt-other", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status=%d", rec.Code)
	}
}
