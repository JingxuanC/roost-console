package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// Admin is the internal operations surface. It is not a second user store: it
// reads and writes the same organizations, wallets, ledger and orders the
// product uses, gated by an email allowlist (ADMIN_EMAILS).

// adminOverviewStore is the cross-organization reporting slice. It is a seam so
// the admin gate and parameter handling can be tested without a database.
type adminOverviewStore interface {
	AdminOverview(ctx context.Context, days int) (billing.AdminOverview, error)
}

// adminGiftBilling is the explicit gift path. Separate from the generic
// adjustment so gift validation and its ledger reason cannot drift.
type adminGiftBilling interface {
	AdminGiftCredits(ctx context.Context, orgID string, credits int64, note, idempotencyKey, operator string) (int64, error)
}

// adminReferralBilling is the platform referral summary (optional operations
// visibility, not part of the user-facing invite flow).
type adminReferralBilling interface {
	AdminReferrals(ctx context.Context, limit int) (billing.AdminReferrals, error)
}

func (s *Server) adminGiftService() adminGiftBilling {
	if s.adminGift != nil {
		return s.adminGift
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) adminReferralService() adminReferralBilling {
	if s.adminReferral != nil {
		return s.adminReferral
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) adminOverviewStore() adminOverviewStore {
	if s.overview != nil {
		return s.overview
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

// parseAdminOverviewDays accepts an optional integer day count. Out-of-range
// values are clamped to 1..365; a non-numeric value is a client error.
func parseAdminOverviewDays(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return billing.AdminOverviewDaysDefault, true
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if days < 1 {
		days = 1
	}
	if days > billing.AdminOverviewDaysMax {
		days = billing.AdminOverviewDaysMax
	}
	return days, true
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	days, ok := parseAdminOverviewDays(r.URL.Query().Get("days"))
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "days must be an integer")
		return
	}
	store := s.adminOverviewStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "admin overview store is not configured")
		return
	}
	out, err := store.AdminOverview(r.Context(), days)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

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

// adminGiftCredits is the explicit "gift credits" operation. Unlike the generic
// adjustment it is positive-only, has its own ledger reason (`admin_gift`), and
// writes the operator + note to billing.admin_audit. The client supplies the
// idempotency key, either as an Idempotency-Key header or in the JSON body, so a
// timeout retry cannot double-gift.
func (s *Server) adminGiftCredits(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Credits        int64  `json:"credits"`
		Note           string `json:"note"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if req.Credits <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_credits", "credits must be a positive integer")
		return
	}
	if len(req.Note) > 500 {
		writeError(w, http.StatusBadRequest, "bad_request", "note is too long")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	}
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "idempotency key is required")
		return
	}
	service := s.adminGiftService()
	if service == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "gift store is not configured")
		return
	}
	user := userFrom(r)
	operator := ""
	if user != nil {
		operator = user.EmailOrID()
	}
	balance, err := service.AdminGiftCredits(r.Context(), r.PathValue("orgID"), req.Credits, req.Note, idempotencyKey, operator)
	if err != nil {
		switch {
		case errors.Is(err, billing.ErrInvalidGiftCredits):
			writeError(w, http.StatusBadRequest, "invalid_credits", err.Error())
		case errors.Is(err, billing.ErrMissingIdempotencyKey):
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		case errors.Is(err, billing.ErrGiftIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
		default:
			s.fail(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"balance_credits": balance,
		"credits":         req.Credits,
		"note":            req.Note,
	})
}

// parseAdminReferralsLimit accepts an optional integer limit, clamped to 1..200.
func parseAdminReferralsLimit(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 50, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	return limit, true
}

func (s *Server) adminReferrals(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseAdminReferralsLimit(r.URL.Query().Get("limit"))
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "limit must be an integer")
		return
	}
	service := s.adminReferralService()
	if service == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "referral store is not configured")
		return
	}
	out, err := service.AdminReferrals(r.Context(), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
