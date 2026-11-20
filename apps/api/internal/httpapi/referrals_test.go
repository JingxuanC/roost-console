package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/auth"
	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

type fakeReferralStore struct {
	org         billing.Org
	orgErr      error
	overview    billing.ReferralOverview
	overviewErr error
	claim       billing.ClaimReferralResult
	claimErr    error

	ensureOrgCalls  []string
	ensureByIDCalls []string
	overviewCalls   []string
	claimCalls      []billing.ClaimReferralInput
}

func (f *fakeReferralStore) EnsureOrg(_ context.Context, userID, _ string) (billing.Org, error) {
	f.ensureOrgCalls = append(f.ensureOrgCalls, userID)
	return f.org, f.orgErr
}

func (f *fakeReferralStore) EnsureOrgByUserID(_ context.Context, userID string) (billing.Org, error) {
	f.ensureByIDCalls = append(f.ensureByIDCalls, userID)
	return f.org, f.orgErr
}

func (f *fakeReferralStore) ReferralOverview(_ context.Context, userID, orgID string, _ int) (billing.ReferralOverview, error) {
	f.overviewCalls = append(f.overviewCalls, userID+"/"+orgID)
	return f.overview, f.overviewErr
}

func (f *fakeReferralStore) ClaimReferral(_ context.Context, in billing.ClaimReferralInput) (billing.ClaimReferralResult, error) {
	f.claimCalls = append(f.claimCalls, in)
	return f.claim, f.claimErr
}

func referralTestHandler(t *testing.T, store referralBilling) http.Handler {
	t.Helper()
	return referralTestHandlerWithConfig(t, store, nil)
}

func referralTestHandlerWithConfig(t *testing.T, store referralBilling, mutate func(*Server)) http.Handler {
	t.Helper()
	supabase := fakeSupabase(t)
	cfg := testConfig()
	cfg.AdminEmails = []string{"owner@example.com"}
	cfg.ReferralInviterCredits = 1000
	cfg.ReferralInviteeCredits = 1000
	s := &Server{
		cfg:        cfg,
		verifier:   auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		membership: membershipFixture(),
		referrals:  store,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mutate != nil {
		mutate(s)
	}
	return s.Handler()
}

// callJSONBody is callJSON with a request body and optional headers.
func callJSONBody(t *testing.T, h http.Handler, method, path, bearer, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
		}
	}
	return rec, out
}

func TestReferralOverviewOwner(t *testing.T) {
	store := &fakeReferralStore{
		org: billing.Org{ID: "org-1", Role: "owner"},
		overview: billing.ReferralOverview{
			Code: "ABCD2345", InvitedCount: 2,
			TotalInviterCredits: 2000, TotalInviteeCredits: 2000,
		},
	}
	h := referralTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/referral", "jwt-owner", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["code"] != "ABCD2345" {
		t.Fatalf("code=%v", body["code"])
	}
	if body["link"] != "https://www.roostbots.com/console/?ref=ABCD2345" {
		t.Fatalf("link=%v", body["link"])
	}
	for key, want := range map[string]float64{
		"invited_count": 2, "inviter_credits": 1000, "invitee_credits": 1000,
		"total_inviter_credits": 2000, "total_invitee_credits": 2000,
	} {
		if body[key] != want {
			t.Fatalf("%s=%v want %v", key, body[key], want)
		}
	}
	if len(store.ensureOrgCalls) != 1 || store.ensureOrgCalls[0] != "user-owner" {
		t.Fatalf("ensure calls=%v", store.ensureOrgCalls)
	}
	if len(store.overviewCalls) != 1 || store.overviewCalls[0] != "user-owner/org-1" {
		t.Fatalf("overview calls=%v", store.overviewCalls)
	}
}

func TestReferralOverviewOwnerOnly(t *testing.T) {
	store := &fakeReferralStore{org: billing.Org{ID: "org-1", Role: "member"}}
	h := referralTestHandler(t, store)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/referral", "jwt-other", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if len(store.overviewCalls) != 0 {
		t.Fatalf("member reached the store: %v", store.overviewCalls)
	}
	if rec, _ := callJSON(t, h, http.MethodGet, "/v1/referral", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", rec.Code)
	}
}

func TestReferralOverviewStoreUnavailable(t *testing.T) {
	h := referralTestHandler(t, nil)
	rec, body := callJSON(t, h, http.MethodGet, "/v1/referral", "jwt-owner", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
}

func TestClaimReferralSuccess(t *testing.T) {
	rewarded := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	store := &fakeReferralStore{
		org: billing.Org{ID: "org-2", Role: "owner"},
		claim: billing.ClaimReferralResult{
			ReferralID: "ref-1", Code: "ABCD2345", Status: "rewarded",
			InviterCredits: 1000, InviteeCredits: 1000, BalanceCredits: 2000,
			RewardedAt: &rewarded,
		},
	}
	h := referralTestHandler(t, store)
	rec, body := callJSONBody(t, h, http.MethodPost, "/v1/referral/claim", "jwt-other", `{"code":"  abcd2345 "}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["ok"] != true || body["already_claimed"] != false || body["status"] != "rewarded" {
		t.Fatalf("body=%v", body)
	}
	if body["balance_credits"] != float64(2000) || body["invitee_credits"] != float64(1000) {
		t.Fatalf("rewards=%v", body)
	}
	if body["rewarded_at"] != "2026-11-20T01:02:03Z" {
		t.Fatalf("rewarded_at=%v", body["rewarded_at"])
	}
	if len(store.ensureByIDCalls) != 1 || store.ensureByIDCalls[0] != "user-other" {
		t.Fatalf("ensure calls=%v", store.ensureByIDCalls)
	}
	if len(store.claimCalls) != 1 {
		t.Fatalf("claim calls=%v", store.claimCalls)
	}
	call := store.claimCalls[0]
	if call.Code != "ABCD2345" || call.InviteeUserID != "user-other" ||
		call.InviteeOrgID != "org-2" || call.InviterCredits != 1000 || call.InviteeCredits != 1000 {
		t.Fatalf("claim input=%+v", call)
	}
}

func TestClaimReferralIdempotentReplay(t *testing.T) {
	store := &fakeReferralStore{
		org: billing.Org{ID: "org-2", Role: "owner"},
		claim: billing.ClaimReferralResult{
			ReferralID: "ref-1", Code: "ABCD2345", Status: "rewarded",
			AlreadyClaimed: true, InviterCredits: 1000, InviteeCredits: 1000, BalanceCredits: 2000,
		},
	}
	h := referralTestHandler(t, store)
	for i := 0; i < 2; i++ {
		rec, body := callJSONBody(t, h, http.MethodPost, "/v1/referral/claim", "jwt-other", `{"code":"ABCD2345"}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%v", i, rec.Code, body)
		}
		if body["already_claimed"] != true {
			t.Fatalf("attempt %d body=%v", i, body)
		}
	}
	if len(store.claimCalls) != 2 {
		t.Fatalf("claim calls=%v", store.claimCalls)
	}
}

func TestClaimReferralValidation(t *testing.T) {
	store := &fakeReferralStore{org: billing.Org{ID: "org-2", Role: "owner"}}
	h := referralTestHandler(t, store)
	cases := []struct {
		name, body string
	}{
		{"empty", ``},
		{"invalid-json", `{`},
		{"missing-code", `{"code":"   "}`},
		{"too-long", `{"code":"` + strings.Repeat("A", 65) + `"}`},
	}
	for _, tc := range cases {
		rec, body := callJSONBody(t, h, http.MethodPost, "/v1/referral/claim", "jwt-other", tc.body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%v", tc.name, rec.Code, body)
		}
	}
	if len(store.claimCalls) != 0 {
		t.Fatalf("invalid claims reached the store: %v", store.claimCalls)
	}
	if rec, _ := callJSONBody(t, h, http.MethodPost, "/v1/referral/claim", "", `{"code":"ABCD2345"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", rec.Code)
	}
}

func TestClaimReferralErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"not-found", billing.ErrReferralCodeNotFound, http.StatusNotFound, "not_found"},
		{"inactive", billing.ErrReferralCodeInactive, http.StatusBadRequest, "referral_not_eligible"},
		{"exhausted", billing.ErrReferralCodeExhausted, http.StatusBadRequest, "referral_not_eligible"},
		{"self", billing.ErrReferralSelfClaim, http.StatusBadRequest, "referral_not_eligible"},
		{"too-old", billing.ErrReferralAccountTooOld, http.StatusBadRequest, "referral_not_eligible"},
		{"unexpected", io.ErrUnexpectedEOF, http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeReferralStore{org: billing.Org{ID: "org-2", Role: "owner"}, claimErr: tc.err}
			h := referralTestHandler(t, store)
			rec, body := callJSONBody(t, h, http.MethodPost, "/v1/referral/claim", "jwt-other", `{"code":"ABCD2345"}`, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%v", rec.Code, tc.want, body)
			}
			errBody, _ := body["error"].(map[string]any)
			if errBody["code"] != tc.code {
				t.Fatalf("error=%v want %s", body, tc.code)
			}
		})
	}
}

func TestClaimReferralRateLimit(t *testing.T) {
	store := &fakeReferralStore{
		org:   billing.Org{ID: "org-2", Role: "owner"},
		claim: billing.ClaimReferralResult{ReferralID: "ref-1", Code: "ABCD2345", Status: "rewarded"},
	}
	h := referralTestHandlerWithConfig(t, store, func(s *Server) { s.cfg.RateReferralClaim = 1 })
	first, _ := callJSONBody(t, h, http.MethodPost, "/v1/referral/claim", "jwt-other", `{"code":"ABCD2345"}`, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d", first.Code)
	}
	second, body := callJSONBody(t, h, http.MethodPost, "/v1/referral/claim", "jwt-other", `{"code":"ABCD2345"}`, nil)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status=%d body=%v", second.Code, body)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "rate_limited" {
		t.Fatalf("error=%v", body)
	}
}
