package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// usageAnalyticsStore is the billing slice behind the org usage dashboard. It
// is an interface so handler tests do not need Postgres.
type usageAnalyticsStore interface {
	UsageSummary(ctx context.Context, orgID string, days int) (billing.UsageSummary, error)
	UsageDaily(ctx context.Context, orgID string, days int) ([]billing.UsageDailyRow, error)
	UsageRecent(ctx context.Context, orgID string, limit int) ([]billing.UsageRecentRow, error)
}

// usageStore falls back to the real store when no test seam is set.
func (s *Server) usageStore() usageAnalyticsStore {
	if s.usage != nil {
		return s.usage
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

// orgUsageSummary serves GET /v1/orgs/{orgID}/usage/summary. The caller must be
// a member of the path's organization; the store query is additionally scoped
// by that org_id.
func (s *Server) orgUsageSummary(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgID")
	if !s.requireOrg(w, r, orgID) {
		return
	}
	days, err := parseUsageDays(r.URL.Query().Get("days"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_days", err.Error())
		return
	}
	store := s.usageStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "usage store is not configured")
		return
	}
	summary, err := store.UsageSummary(r.Context(), orgID, days)
	if err != nil {
		s.fail(w, err)
		return
	}
	summary.Days = days
	writeJSON(w, http.StatusOK, summary)
}

// orgUsageDaily serves GET /v1/orgs/{orgID}/usage/daily. Buckets are oldest
// first and only include days with data; the frontend fills zero days.
func (s *Server) orgUsageDaily(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgID")
	if !s.requireOrg(w, r, orgID) {
		return
	}
	days, err := parseUsageDays(r.URL.Query().Get("days"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_days", err.Error())
		return
	}
	store := s.usageStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "usage store is not configured")
		return
	}
	rows, err := store.UsageDaily(r.Context(), orgID, days)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rows == nil {
		// Keep the wire shape stable: an empty series is [], not null.
		rows = []billing.UsageDailyRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": rows})
}

// orgUsageRecent serves GET /v1/orgs/{orgID}/usage/recent, newest first.
func (s *Server) orgUsageRecent(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgID")
	if !s.requireOrg(w, r, orgID) {
		return
	}
	limit, err := parseUsageLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	store := s.usageStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "usage store is not configured")
		return
	}
	rows, err := store.UsageRecent(r.Context(), orgID, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rows == nil {
		rows = []billing.UsageRecentRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "limit": limit})
}

// parseUsageDays accepts an absent days (the default), any integer (clamped to
// the supported 1..365 window), and rejects non-numeric input.
func parseUsageDays(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return billing.UsageDaysDefault, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("days must be an integer")
	}
	if n < 1 {
		return 1, nil
	}
	if n > billing.UsageDaysMax {
		return billing.UsageDaysMax, nil
	}
	return n, nil
}

// parseUsageLimit mirrors parseUsageDays for the recent-rows page size.
func parseUsageLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return billing.UsageRecentDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if n < 1 {
		return 1, nil
	}
	if n > billing.UsageRecentMaxLimit {
		return billing.UsageRecentMaxLimit, nil
	}
	return n, nil
}
