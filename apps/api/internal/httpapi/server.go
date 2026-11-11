package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/auth"
	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
	"github.com/JingxuanC/roost-console/apps/api/internal/config"
	"github.com/JingxuanC/roost-console/apps/api/internal/payment"
)

type Server struct {
	cfg      config.Config
	store    *billing.Store
	verifier *auth.Verifier
	pay      *payment.Client
	log      *slog.Logger
}

type ctxKey string

const userKey ctxKey = "user"

func New(cfg config.Config, store *billing.Store, verifier *auth.Verifier, pay *payment.Client, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, store: store, verifier: verifier, pay: pay, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /v1/me", s.withUser(s.me))
	mux.HandleFunc("GET /v1/billing/plans", s.withUser(s.plans))
	mux.HandleFunc("GET /v1/orgs/{orgID}/billing", s.withUser(s.billingView))
	mux.HandleFunc("GET /v1/billing/orders", s.withUser(s.listOrders))
	mux.HandleFunc("POST /v1/billing/orders", s.withUser(s.createOrder))
	mux.HandleFunc("GET /v1/billing/orders/{orderID}", s.withUser(s.getOrder))
	mux.HandleFunc("POST /v1/payments/xunhupay/notify", s.xunhuNotify)
	mux.HandleFunc("POST /v1/dev/orders/{orderID}/pay", s.withUser(s.devPay))
	mux.HandleFunc("GET /api/auth/billing/leases/capability", s.withCompatService(s.compatCapability))
	mux.HandleFunc("POST /api/auth/validate", s.withCompatService(s.compatValidate))
	mux.HandleFunc("POST /api/auth/billing/leases/acquire", s.withCompatService(s.compatAcquire))
	mux.HandleFunc("POST /api/auth/billing/leases/release", s.withCompatService(s.compatRelease))
	mux.HandleFunc("POST /internal/v1/orgs/resolve-user", s.withService(s.resolveUserOrg))
	mux.HandleFunc("POST /internal/v1/entitlements/resolve", s.withService(s.resolveEntitlement))
	mux.HandleFunc("POST /internal/v1/usage/leases", s.withService(s.reserve))
	mux.HandleFunc("POST /internal/v1/usage/leases/{leaseID}/settle", s.withService(s.settle))
	mux.HandleFunc("POST /internal/v1/usage/leases/{leaseID}/release", s.withService(s.release))
	return s.cors(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "roost-console", "time": time.Now().UTC()})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := origin == s.cfg.ConsoleOrigin || strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:")
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Requested-With")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		user, err := s.verifier.User(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid session")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	}
}

func (s *Server) withService(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "could not read body")
			return
		}
		if subtle.ConstantTimeCompare([]byte(bearer(r)), []byte(s.cfg.ServiceSecret)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "bad service token")
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		next(w, r)
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func userFrom(r *http.Request) *auth.User {
	u, _ := r.Context().Value(userKey).(*auth.User)
	return u
}

func (s *Server) requireOrg(w http.ResponseWriter, r *http.Request, orgID string) bool {
	role, err := s.store.Membership(r.Context(), orgID, userFrom(r).ID)
	if err != nil || role == "" {
		writeError(w, http.StatusForbidden, "forbidden", "not a member of this organization")
		return false
	}
	return true
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	org, err := s.store.EnsureOrg(r.Context(), u.ID, u.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	view, err := s.store.Billing(r.Context(), org.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "org": org, "billing": view})
}

func (s *Server) plans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.Plans(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (s *Server) billingView(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgID")
	if !s.requireOrg(w, r, orgID) {
		return
	}
	view, err := s.store.Billing(r.Context(), orgID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	org, err := s.store.EnsureOrg(r.Context(), u.ID, u.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	orders, err := s.store.ListOrders(r.Context(), org.ID, 50)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

type createOrderRequest struct {
	SKU string `json:"sku"`
}

type orderResponse struct {
	*billing.Order
	QRURL  string `json:"qr_url,omitempty"`
	PayURL string `json:"pay_url,omitempty"`
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	org, err := s.store.EnsureOrg(r.Context(), u.ID, u.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req createOrderRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.SKU == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "sku is required")
		return
	}
	order, err := s.store.CreateOrder(r.Context(), org.ID, req.SKU)
	if err != nil {
		s.fail(w, err)
		return
	}
	pack, _, err := s.store.Quote(r.Context(), req.SKU)
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.cfg.PaymentConfigured() {
		res, err := s.pay.Create(payment.CreateInput{OutTradeNo: order.OutTradeNo, AmountCents: pack.AmountCents, Title: pack.Title, Attach: org.ID})
		if err != nil {
			s.log.Error("xunhupay create failed", "err", err, "order", order.ID)
			writeError(w, http.StatusBadGateway, "payment_unavailable", "payment provider unavailable")
			return
		}
		if err := s.store.AttachPayment(r.Context(), order.ID, res.ProviderOrderID, res.QRURL, res.PayURL); err != nil {
			s.fail(w, err)
			return
		}
		order.QRURL, order.PayURL, order.Status = res.QRURL, res.PayURL, "pending"
	} else if s.cfg.Environment == "development" {
		// Local development without merchant credentials: expose an explicit
		// dev-only payment action instead of pretending a QR code exists.
		_ = s.store.AttachPayment(r.Context(), order.ID, "dev_"+order.ID, "", s.cfg.PublicBaseURL+"/dev-pay?order="+order.ID)
		order.PayURL, order.Status = s.cfg.PublicBaseURL+"/dev-pay?order="+order.ID, "pending"
	} else {
		writeError(w, http.StatusServiceUnavailable, "payment_not_configured", "payment provider is not configured")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": order})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	org, err := s.store.EnsureOrg(r.Context(), u.ID, u.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	o, err := s.store.GetOrder(r.Context(), org.ID, r.PathValue("orderID"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": o})
}

func (s *Server) devPay(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Environment != "development" {
		http.NotFound(w, r)
		return
	}
	orderID := r.PathValue("orderID")
	u := userFrom(r)
	if u == nil {
		var req struct {
			Email string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeError(w, http.StatusUnauthorized, "unauthorized", "dev pay requires a session")
		return
	}
	org, err := s.store.EnsureOrg(r.Context(), u.ID, u.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	o, err := s.store.GetOrder(r.Context(), org.ID, orderID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.store.Fulfill(r.Context(), o.ID, "dev_"+o.ID, o.AmountCents, []byte(`{"dev":true}`)); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) xunhuNotify(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !payment.Verify(r.PostForm, s.cfg.XunhuAppSecret) {
		s.log.Warn("xunhupay notify bad signature")
		http.Error(w, "bad sign", http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("status") != "OD" {
		// Refunds are handled manually in v1; acknowledge and leave the order.
		_, _ = w.Write([]byte("success"))
		return
	}
	tradeNo := r.PostForm.Get("trade_order_id")
	o, err := s.store.OrderByTradeNo(r.Context(), tradeNo)
	if err != nil {
		s.log.Warn("xunhupay notify unknown order", "trade_no", tradeNo)
		http.Error(w, "unknown order", http.StatusNotFound)
		return
	}
	amount, err := payment.AmountCentsFromYuan(r.PostForm.Get("total_fee"))
	if err != nil {
		http.Error(w, "bad amount", http.StatusBadRequest)
		return
	}
	raw, _ := json.Marshal(r.PostForm)
	if err := s.store.Fulfill(r.Context(), o.ID, r.PostForm.Get("transaction_id"), amount, raw); err != nil {
		s.log.Error("xunhupay fulfill failed", "order", o.ID, "err", err)
		http.Error(w, "fulfill failed", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte("success"))
}

type leaseRequest struct {
	OrgID             string `json:"org_id"`
	Product           string `json:"product"`
	RunRef            string `json:"run_ref"`
	EstimatedCredits  int64  `json:"estimated_credits"`
	TTLSeconds        int    `json:"ttl_seconds"`
}

func (s *Server) withCompatService(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Service-Token")), []byte(s.cfg.ServiceSecret)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "bad service token")
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-Id"))
		if userID == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "X-User-Id is required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, &auth.User{ID: userID})))
	}
}

func (s *Server) compatCapability(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "lease_ttl_seconds": 300,
		"rate_multiplier_min": 0.1, "rate_multiplier_max": 100.0,
	})
}

func accessTier(code string) int {
	switch code {
	case "max":
		return 3
	case "pro", "byok-pro":
		return 2
	case "starter":
		return 1
	default:
		return 0
	}
}

func (s *Server) compatValidate(w http.ResponseWriter, r *http.Request) {
	userID := userFrom(r).ID
	org, err := s.store.EnsureOrgByUserID(r.Context(), userID)
	if err != nil {
		s.fail(w, err)
		return
	}
	e, err := s.store.Resolve(r.Context(), org.ID, "dsalpha")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"access_tier": -1, "plan_display_name": "", "scopes": []string{}, "quota": map[string]any{"allowed": false, "limit_type": "plan", "message": "Roost 组织未开通投研产品"}})
		return
	}
	var req struct {
		CheckQuota string `json:"check_quota"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	allowed := e.Available > 0
	limitType := "credit_limit"
	if req.CheckQuota == "computer_count" || req.CheckQuota == "workspace" {
		allowed = true
		limitType = "workspace_limit"
	}
	quota := map[string]any{
		"allowed": allowed, "limit_type": limitType,
		"used_credits": e.Balance - e.Available, "credit_limit": e.Balance,
		"remaining_credits": e.Available, "message": "Roost 积分不足，请充值",
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_tier": accessTier(e.PlanCode), "plan_display_name": e.PlanCode,
		"scopes": []string{}, "quota": quota,
	})
}

func (s *Server) compatAcquire(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID       string  `json:"user_id"`
		RunRef       string  `json:"run_ref"`
		SpentCredits float64 `json:"spent_credits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RunRef == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "run_ref is required")
		return
	}
	org, err := s.store.EnsureOrgByUserID(r.Context(), req.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	estimate := int64(math.Ceil(math.Max(200, req.SpentCredits*2+200)))
	lease, err := s.store.Reserve(r.Context(), org.ID, "dsalpha", req.RunRef, estimate, 300*time.Second)
	if err != nil {
		if errors.Is(err, billing.ErrInsufficientCredits) || errors.Is(err, billing.ErrProductCap) || errors.Is(err, billing.ErrProductNotEnabled) {
			writeJSON(w, http.StatusOK, map[string]any{"granted": false, "quota": map[string]any{"allowed": false, "message": "Roost 积分不足，请充值", "limit_type": "credit_limit"}})
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"granted": true, "ceiling_credits": lease.ReservedCredits,
		"expires_in_seconds": 300, "generation": 1,
		"quota": map[string]any{"allowed": true},
	})
}

func (s *Server) compatRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID       string  `json:"user_id"`
		RunRef       string  `json:"run_ref"`
		SpentCredits float64 `json:"spent_credits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RunRef == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "run_ref is required")
		return
	}
	org, err := s.store.EnsureOrgByUserID(r.Context(), req.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	actual := int64(math.Ceil(math.Max(0, req.SpentCredits)))
	if actual > 0 {
		_, err = s.store.SettleByRun(r.Context(), org.ID, "dsalpha", req.RunRef, actual)
	} else {
		err = s.store.ReleaseByRun(r.Context(), org.ID, "dsalpha", req.RunRef)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": true})
}

func (s *Server) resolveUserOrg(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "user_id is required")
		return
	}
	org, err := s.store.OrgForUser(r.Context(), req.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

func (s *Server) resolveEntitlement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID   string `json:"org_id"`
		Product string `json:"product"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID == "" || req.Product == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "org_id and product are required")
		return
	}
	e, err := s.store.Resolve(r.Context(), req.OrgID, req.Product)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) reserve(w http.ResponseWriter, r *http.Request) {
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID == "" || req.Product == "" || req.RunRef == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "org_id, product and run_ref are required")
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	l, err := s.store.Reserve(r.Context(), req.OrgID, req.Product, req.RunRef, req.EstimatedCredits, ttl)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

func (s *Server) settle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ActualCredits int64 `json:"actual_credits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "actual_credits is required")
		return
	}
	l, err := s.store.Settle(r.Context(), r.PathValue("leaseID"), req.ActualCredits)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Release(r.Context(), r.PathValue("leaseID")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": true})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, billing.ErrInsufficientCredits):
		writeError(w, http.StatusPaymentRequired, "insufficient_credits", "insufficient credits")
	case errors.Is(err, billing.ErrProductCap):
		writeError(w, http.StatusTooManyRequests, "product_cap", "product monthly cap reached")
	case errors.Is(err, billing.ErrProductNotEnabled):
		writeError(w, http.StatusForbidden, "product_disabled", "product is not enabled")
	default:
		s.log.Error("api error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

var _ = fmt.Sprintf
var _ = strconv.Itoa
