package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// The relay introspect response keeps subject_ref = org id for every existing
// client, and adds the issuing user as user_id. A legacy token (issued before
// 0012_product_token_user.sql) has no user binding: user_id must still be
// present but empty, so the relay can fall back to org-scoped ACP state instead
// of rejecting the token.
func TestRelayIntrospectReturnsOrgAndUser(t *testing.T) {
	f := &fakeRelayBilling{identity: billing.RelayIdentity{
		OrgID:  "org-1",
		UserID: "9f5b7a5e-2f4a-4a4a-8a0f-6a7f3f2b1c9d",
	}}
	body := []byte(`{"api_key":"rk_test"}`)
	req := signedRelayRequest(http.MethodPost, "/v1/relay/api-keys/introspect", body)
	rec := httptest.NewRecorder()
	relayHTTPTestServer(f).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decodeRelayResponse(t, rec)
	if out["subject_ref"] != "org-1" {
		t.Fatalf("subject_ref=%v", out["subject_ref"])
	}
	if out["user_id"] != "9f5b7a5e-2f4a-4a4a-8a0f-6a7f3f2b1c9d" {
		t.Fatalf("user_id=%v", out["user_id"])
	}
	if out["balance_minor"] != float64(1000*RelayMilliPerCredit) {
		t.Fatalf("balance_minor=%v", out["balance_minor"])
	}
}

func TestRelayIntrospectLegacyTokenHasEmptyUserID(t *testing.T) {
	f := &fakeRelayBilling{}
	body := []byte(`{"api_key":"rk_legacy"}`)
	req := signedRelayRequest(http.MethodPost, "/v1/relay/api-keys/introspect", body)
	rec := httptest.NewRecorder()
	relayHTTPTestServer(f).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decodeRelayResponse(t, rec)
	if out["subject_ref"] != "org-1" {
		t.Fatalf("subject_ref=%v", out["subject_ref"])
	}
	userID, ok := out["user_id"]
	if !ok {
		t.Fatalf("legacy introspect response must include user_id: %v", out)
	}
	if userID != "" {
		t.Fatalf("legacy user_id=%v, want empty string", userID)
	}
}

func TestRelayIntrospectUnknownKey(t *testing.T) {
	f := &fakeRelayBilling{apiKeyErr: billing.ErrRelayNotFound}
	body := []byte(`{"api_key":"rk_unknown"}`)
	req := signedRelayRequest(http.MethodPost, "/v1/relay/api-keys/introspect", body)
	rec := httptest.NewRecorder()
	relayHTTPTestServer(f).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
