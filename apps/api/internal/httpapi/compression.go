package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// compressionUsageStore is the billing slice behind the org compression
// report. It is an interface so handler tests do not need Postgres.
type compressionUsageStore interface {
	CompressionUsage(ctx context.Context, orgID string, limit int) (billing.CompressionUsage, error)
}

// compressionStore falls back to the real store when no test seam is set.
func (s *Server) compressionStore() compressionUsageStore {
	if s.compression != nil {
		return s.compression
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

// orgCompressionUsage serves GET /v1/orgs/{orgID}/billing/compression. The
// caller must be a member of the path's organization; the store query is
// additionally scoped by that org_id, so another organization's rows are never
// returned.
func (s *Server) orgCompressionUsage(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgID")
	if !s.requireOrg(w, r, orgID) {
		return
	}
	limit, err := parseCompressionLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	store := s.compressionStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "compression store is not configured")
		return
	}
	usage, err := store.CompressionUsage(r.Context(), orgID, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	if usage.Rows == nil {
		// Keep the wire shape stable: an empty report is [], not null.
		usage.Rows = []billing.CompressionRow{}
	}
	writeJSON(w, http.StatusOK, usage)
}

// parseCompressionLimit accepts an absent limit (the store's default), a
// positive integer, or a too-large integer that is clamped to the store cap.
// Zero, negative and non-numeric values are rejected instead of silently
// changing the result set.
func parseCompressionLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return billing.CompressionDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("limit must be a positive integer")
	}
	if n > billing.CompressionMaxLimit {
		n = billing.CompressionMaxLimit
	}
	return n, nil
}
