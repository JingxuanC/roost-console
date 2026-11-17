package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// Relay endpoints implement the platform side of the relay-aggregator
// contract: introspect a downstream rk- key, hold a request's estimated spend,
// settle actual usage, release unspent holds and answer idempotency queries.
// Every route is service-authenticated; the same secret may arrive as the
// signed service headers or as the legacy X-Service-Token header.

func (s *Server) relayIntrospect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
	}
	if !decodeRelayBody(w, r, &req) {
		return
	}
	if req.APIKey == "" {
		writeRelayError(w, http.StatusBadRequest, "invalid_request", "api_key is required")
		return
	}
	orgID, err := s.store.RelayAPIKey(r.Context(), req.APIKey)
	if err != nil {
		if errors.Is(err, billing.ErrRelayNotFound) {
			writeRelayError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
			return
		}
		s.fail(w, err)
		return
	}
	balance, err := s.store.RelayBalance(r.Context(), orgID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subject_ref":   orgID,
		"balance_minor": balance,
		"currency":      billing.RelayCurrency,
		"as_of":         time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) relayReserve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID      string `json:"request_id"`
		IdempotencyKey string `json:"idempotency_key"`
		SubjectRef     string `json:"subject_ref"`
		AmountMinor    int64  `json:"amount_minor"`
		Currency       string `json:"currency"`
		TTLSeconds     int64  `json:"ttl_seconds"`
		Reason         string `json:"reason"`
	}
	if !decodeRelayBody(w, r, &req) {
		return
	}
	if req.RequestID == "" || req.SubjectRef == "" {
		writeRelayError(w, http.StatusBadRequest, "invalid_request", "request_id and subject_ref are required")
		return
	}
	if req.Currency != billing.RelayCurrency {
		writeRelayError(w, http.StatusBadRequest, "currency_mismatch", "unsupported currency")
		return
	}
	reservation, err := s.store.RelayReserve(r.Context(), req.SubjectRef, req.RequestID, req.AmountMinor, req.Currency, time.Duration(req.TTLSeconds)*time.Second)
	if err != nil {
		relayStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reservation_id": reservation.ID,
		"amount_minor":   reservation.Amount,
		"currency":       billing.RelayCurrency,
		"expires_at":     reservation.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) relaySettle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SettlementID   string `json:"settlement_id"`
		IdempotencyKey string `json:"idempotency_key"`
		AmountMinor    int64  `json:"amount_minor"`
		Currency       string `json:"currency"`
		UsageStatus    string `json:"usage_status"`
	}
	if !decodeRelayBody(w, r, &req) {
		return
	}
	if req.IdempotencyKey == "" {
		writeRelayError(w, http.StatusBadRequest, "invalid_request", "idempotency_key is required")
		return
	}
	if req.Currency != "" && req.Currency != billing.RelayCurrency {
		writeRelayError(w, http.StatusBadRequest, "currency_mismatch", "unsupported currency")
		return
	}
	op, err := s.store.RelaySettle(r.Context(), r.PathValue("reservationID"), req.IdempotencyKey, req.AmountMinor)
	if err != nil {
		relayStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relayOperationBody(op))
}

func (s *Server) relayRelease(w http.ResponseWriter, r *http.Request) {
	op, err := s.store.RelayRelease(r.Context(), r.PathValue("reservationID"))
	if err != nil {
		relayStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relayOperationBody(op))
}

func (s *Server) relayOperation(w http.ResponseWriter, r *http.Request) {
	op, err := s.store.RelayOperation(r.Context(), r.PathValue("idempotencyKey"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !op.Found {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, relayOperationBody(op))
}

func relayOperationBody(op billing.RelayOperation) map[string]any {
	return map[string]any{
		"found":          op.Found,
		"operation":      op.Operation,
		"status":         op.Status,
		"reservation_id": op.ReservationID,
		"amount_minor":   op.Amount,
		"error_code":     op.ErrorCode,
	}
}

func decodeRelayBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeRelayError(w, http.StatusBadRequest, "bad_request", "could not read body")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeRelayError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return false
	}
	return true
}

func relayStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrInsufficientCredits):
		writeRelayError(w, http.StatusPaymentRequired, "insufficient_balance", "insufficient balance")
	case errors.Is(err, billing.ErrRelayConflict):
		writeRelayError(w, http.StatusConflict, "conflict", "idempotency conflict")
	case errors.Is(err, billing.ErrRelayNotFound):
		writeRelayError(w, http.StatusNotFound, "not_found", "reservation not found")
	case errors.Is(err, billing.ErrRelayReleased):
		writeRelayError(w, http.StatusConflict, "reservation_released", "reservation already released")
	default:
		writeRelayError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

func writeRelayError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}
