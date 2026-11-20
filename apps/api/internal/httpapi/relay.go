package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// RelayMilliPerCredit is the relay's minor unit: 1000 minor units = 1 credit.
// The relay estimates and settles in milli-credits so short requests are not
// rounded to a free call; the console stores whole credits.
const RelayMilliPerCredit = 1000

// relayBilling is the slice of the billing store used by relay handlers. It is
// an interface so handler tests can exercise the API without Postgres.
type relayBilling interface {
	RelayAPIKey(ctx context.Context, token string) (billing.RelayIdentity, error)
	RelayBalance(ctx context.Context, orgID string) (int64, error)
	RelayReserve(ctx context.Context, orgID, requestID string, amount int64, currency string, ttl time.Duration) (billing.RelayReservation, error)
	RelaySettleWithDetails(ctx context.Context, reservationID, settlementID, idempotencyKey string, amount int64, toolCharges []billing.ToolCharge, compression *billing.Compression, usage *billing.UsageEvent) (billing.RelayOperation, error)
	RelayRelease(ctx context.Context, reservationID string) (billing.RelayOperation, error)
	RelayOperation(ctx context.Context, idempotencyKey string) (billing.RelayOperation, error)
	RelayToolCharges(ctx context.Context, reservationID string) ([]billing.ToolCharge, error)
}

func relayCreditsFromMinor(minor int64) int64 {
	if minor <= 0 {
		return 0
	}
	return (minor + RelayMilliPerCredit - 1) / RelayMilliPerCredit
}

// Relay endpoints implement the platform side of the relay-aggregator
// contract: introspect a downstream rk- key, hold a request's estimated spend,
// settle actual usage, release unspent holds and answer idempotency queries.
// Every route is service-authenticated; the same secret may arrive as the
// signed service headers or as the legacy X-Service-Token header.

func (s *Server) relayStore() relayBilling {
	if s.relay != nil {
		return s.relay
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

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
	identity, err := s.relayStore().RelayAPIKey(r.Context(), req.APIKey)
	if err != nil {
		if errors.Is(err, billing.ErrRelayNotFound) {
			writeRelayError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
			return
		}
		s.fail(w, err)
		return
	}
	balance, err := s.relayStore().RelayBalance(r.Context(), identity.OrgID)
	if err != nil {
		s.fail(w, err)
		return
	}
	// subject_ref stays the org id (the billing tenant) for backward
	// compatibility. user_id is the issuing console user and is "" for legacy
	// tokens issued before user binding; the relay must fall back to
	// org-scoped behavior in that case.
	writeJSON(w, http.StatusOK, map[string]any{
		"subject_ref":   identity.OrgID,
		"user_id":       identity.UserID,
		"balance_minor": balance * RelayMilliPerCredit,
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
	reservation, err := s.relayStore().RelayReserve(r.Context(), req.SubjectRef, req.RequestID, relayCreditsFromMinor(req.AmountMinor), req.Currency, time.Duration(req.TTLSeconds)*time.Second)
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
		SettlementID   string               `json:"settlement_id"`
		IdempotencyKey string               `json:"idempotency_key"`
		AmountMinor    int64                `json:"amount_minor"`
		Currency       string               `json:"currency"`
		UsageStatus    string               `json:"usage_status"`
		ToolCharges    []billing.ToolCharge `json:"tool_charges"`
		Compression    *billing.Compression `json:"compression"`
		Usage          *billing.UsageEvent  `json:"usage"`
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
	// Validate the complete breakdown before touching the store: a malformed
	// entry must not book a settlement (or partially persist tool charges).
	charges, err := billing.NormalizeToolCharges(req.ToolCharges)
	if err != nil {
		writeRelayError(w, http.StatusBadRequest, "invalid_tool_charges", err.Error())
		return
	}
	// Compression is optional. A malformed object is rejected before the store
	// for the same reason; absent or all-zero savings produce no row.
	compression, err := billing.NormalizeCompression(req.Compression)
	if err != nil {
		writeRelayError(w, http.StatusBadRequest, "invalid_compression", err.Error())
		return
	}
	// Usage is optional too. Validate the complete object before booking so a
	// malformed counter never leaves a settlement without its usage row.
	usage, err := billing.NormalizeUsage(req.Usage)
	if err != nil {
		writeRelayError(w, http.StatusBadRequest, "invalid_usage", err.Error())
		return
	}
	op, err := s.relayStore().RelaySettleWithDetails(r.Context(), r.PathValue("reservationID"), req.SettlementID, req.IdempotencyKey, relayCreditsFromMinor(req.AmountMinor), charges, compression, usage)
	if err != nil {
		relayStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relayOperationBody(op))
}

func (s *Server) relayRelease(w http.ResponseWriter, r *http.Request) {
	op, err := s.relayStore().RelayRelease(r.Context(), r.PathValue("reservationID"))
	if err != nil {
		relayStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relayOperationBody(op))
}

func (s *Server) relayOperation(w http.ResponseWriter, r *http.Request) {
	op, err := s.relayStore().RelayOperation(r.Context(), r.PathValue("idempotencyKey"))
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

// relayReservationToolCharges exposes a reservation's persisted breakdown
// directly, for reporting without replaying the settle idempotency key.
func (s *Server) relayReservationToolCharges(w http.ResponseWriter, r *http.Request) {
	reservationID := r.PathValue("reservationID")
	charges, err := s.relayStore().RelayToolCharges(r.Context(), reservationID)
	if err != nil {
		relayStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relayToolChargesBody(reservationID, charges))
}

func relayToolChargesBody(reservationID string, charges []billing.ToolCharge) map[string]any {
	if charges == nil {
		charges = []billing.ToolCharge{}
	}
	totals := billing.SumToolCharges(charges)
	return map[string]any{
		"reservation_id":                   reservationID,
		"tool_charges":                     charges,
		"tool_charges_total":               totals,
		"tool_charges_amount_minor":        totals.AmountMinor,
		"tool_charges_provider_cost_micro": totals.ProviderCostMicro,
	}
}

func relayOperationBody(op billing.RelayOperation) map[string]any {
	charges := op.ToolCharges
	if charges == nil {
		charges = []billing.ToolCharge{}
	}
	totals := billing.SumToolCharges(charges)
	return map[string]any{
		"found":                            op.Found,
		"operation":                        op.Operation,
		"status":                           op.Status,
		"reservation_id":                   op.ReservationID,
		"amount_minor":                     op.Amount * RelayMilliPerCredit,
		"error_code":                       op.ErrorCode,
		"tool_charges":                     charges,
		"tool_charges_total":               totals,
		"tool_charges_amount_minor":        totals.AmountMinor,
		"tool_charges_provider_cost_micro": totals.ProviderCostMicro,
		"compression":                      op.Compression,
		"usage":                            op.Usage,
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
	case errors.Is(err, billing.ErrInvalidToolCharges):
		writeRelayError(w, http.StatusBadRequest, "invalid_tool_charges", err.Error())
	case errors.Is(err, billing.ErrInvalidCompression):
		writeRelayError(w, http.StatusBadRequest, "invalid_compression", err.Error())
	case errors.Is(err, billing.ErrInvalidUsage):
		writeRelayError(w, http.StatusBadRequest, "invalid_usage", err.Error())
	default:
		writeRelayError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

func writeRelayError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

// withRelayService authenticates the relay service itself. Unlike the dslpha
// compatibility middleware it carries no user identity: the relay presents a
// downstream API key, not a user session, and every relay endpoint resolves
// its subject from that key or the reservation.
func (s *Server) withRelayService(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeRelayError(w, http.StatusBadRequest, "bad_request", "could not read body")
			return
		}
		service := "relay"
		verified := s.verifyService(service, r, body)
		if !verified {
			legacy := r.Header.Get("X-Service-Token")
			if s.verifyLegacyServiceToken(service, legacy) {
				verified = true
			}
		}
		if !verified {
			writeRelayError(w, http.StatusUnauthorized, "unauthorized", "bad service signature")
			return
		}
		if !serviceAllows(service, r.Method, r.URL.Path) {
			writeRelayError(w, http.StatusForbidden, "forbidden", "service scope does not allow this endpoint")
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		next(w, r)
	}
}
