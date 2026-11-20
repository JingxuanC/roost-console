package httpapi

import (
	"context"
	"net/http"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// billingDetailsStore is the reporting slice behind the end-user billing detail
// page. It is an interface so handler tests do not need Postgres.
type billingDetailsStore interface {
	BillingDetails(ctx context.Context, orgID string, days int) (billing.BillingDetails, error)
}

func (s *Server) billingDetailsStore() billingDetailsStore {
	if s.billingDetails != nil {
		return s.billingDetails
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

// orgBillingDetails serves GET /v1/orgs/{orgID}/billing/details. Authorization
// matches the other organization usage endpoints: a Supabase session plus
// membership in the organization in the path. The store query is additionally
// scoped by that org_id.
func (s *Server) orgBillingDetails(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgID")
	if !s.requireOrg(w, r, orgID) {
		return
	}
	days, err := parseUsageDays(r.URL.Query().Get("days"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_days", err.Error())
		return
	}
	store := s.billingDetailsStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "billing details store is not configured")
		return
	}
	out, err := store.BillingDetails(r.Context(), orgID, days)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Keep the wire shape stable when the store returns no rows.
	if out.Tokens == nil {
		out.Tokens = []billing.BillingTokenRow{}
	}
	if out.Sandboxes == nil {
		out.Sandboxes = []billing.BillingSandboxRow{}
	}
	writeJSON(w, http.StatusOK, out)
}
