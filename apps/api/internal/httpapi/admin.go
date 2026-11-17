package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Admin is the internal operations surface. It is not a second user store: it
// reads and writes the same organizations, wallets, ledger and orders the
// product uses, gated by an email allowlist (ADMIN_EMAILS).

func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r)
		if user == nil || !s.isAdminEmail(user.Email) {
			writeError(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		next(w, r)
	}
}

func (s *Server) isAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, allowed := range s.cfg.AdminEmails {
		if allowed == email {
			return true
		}
	}
	return false
}

func (s *Server) adminSearchOrgs(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	orgs, err := s.store.AdminSearchOrgs(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orgs": orgs})
}

func (s *Server) adminGetOrg(w http.ResponseWriter, r *http.Request) {
	org, err := s.store.AdminOrg(r.Context(), r.PathValue("orgID"))
	if err != nil {
		s.fail(w, err)
		return
	}
	billingView, err := s.store.Billing(r.Context(), org.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	ledger, err := s.store.AdminLedger(r.Context(), org.ID, 20)
	if err != nil {
		s.fail(w, err)
		return
	}
	orders, err := s.store.ListOrders(r.Context(), org.ID, 20)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"org":     org,
		"billing": billingView,
		"ledger":  ledger,
		"orders":  orders,
	})
}

func (s *Server) adminAdjustCredits(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeltaCredits   int64  `json:"delta_credits"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if req.Reason == "" {
		req.Reason = "operator adjustment"
	}
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "idempotency_key is required")
		return
	}
	user := userFrom(r)
	operator := ""
	if user != nil {
		operator = user.EmailOrID()
	}
	balance, err := s.store.AdminAdjustCredits(r.Context(), r.PathValue("orgID"), req.DeltaCredits, req.Reason, req.IdempotencyKey, operator)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "balance_credits": balance})
}

func (s *Server) adminLedger(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.AdminLedger(r.Context(), r.PathValue("orgID"), 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ledger": rows})
}

func (s *Server) adminOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.store.ListOrders(r.Context(), r.PathValue("orgID"), 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}
