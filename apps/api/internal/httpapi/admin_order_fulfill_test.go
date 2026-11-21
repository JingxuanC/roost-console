package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/JingxuanC/roost-console/apps/api/internal/auth"
	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

type fulfillCall struct {
	orderID string
	tradeNo string
	amount  int
	raw     string
}

type fakeOrderAdmin struct {
	order      billing.Order
	calls      []fulfillCall
	lookupErr  error
	fulfillErr error
}

func (f *fakeOrderAdmin) GetOrderByID(_ context.Context, orderID string) (billing.Order, error) {
	if f.lookupErr != nil {
		return billing.Order{}, f.lookupErr
	}
	if orderID != f.order.ID {
		return billing.Order{}, billing.ErrNotFound
	}
	return f.order, nil
}

func (f *fakeOrderAdmin) Fulfill(_ context.Context, orderID, tradeNo string, amountCents int, raw []byte) error {
	if f.fulfillErr != nil {
		return f.fulfillErr
	}
	f.calls = append(f.calls, fulfillCall{orderID: orderID, tradeNo: tradeNo, amount: amountCents, raw: string(raw)})
	f.order.Status = "fulfilled"
	return nil
}

func adminFulfillHandler(t *testing.T, store *fakeOrderAdmin) http.Handler {
	t.Helper()
	supabase := fakeSupabase(t)
	cfg := testConfig()
	cfg.AdminEmails = []string{"owner@example.com"}
	return (&Server{
		cfg:        cfg,
		verifier:   auth.NewVerifier(supabase.URL, "console-anon-key", supabase.Client()),
		membership: membershipFixture(),
		orderAdmin: store,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
}

func pendingOrder() billing.Order {
	return billing.Order{ID: "order-1", OrgID: "org-1", OutTradeNo: "TRADE1", SKU: "credit_100", AmountCents: 10000, Credits: 10000, Status: "pending"}
}

func TestAdminFulfillOrderSuccess(t *testing.T) {
	store := &fakeOrderAdmin{order: pendingOrder()}
	h := adminFulfillHandler(t, store)
	rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orders/order-1/fulfill", "jwt-owner",
		`{"provider_trade_no":"4200001234"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["ok"] != true {
		t.Fatalf("body=%v", body)
	}
	if len(store.calls) != 1 {
		t.Fatalf("calls=%v", store.calls)
	}
	call := store.calls[0]
	if call.orderID != "order-1" || call.tradeNo != "4200001234" || call.amount != 10000 {
		t.Fatalf("call=%+v", call)
	}
	if !strings.Contains(call.raw, `"manual":true`) {
		t.Fatalf("raw=%s", call.raw)
	}
	if order, _ := body["order"].(map[string]any); order["status"] != "fulfilled" {
		t.Fatalf("order=%v", body["order"])
	}
}

func TestAdminFulfillOrderAlreadyFulfilledIsIdempotent(t *testing.T) {
	store := &fakeOrderAdmin{order: pendingOrder()}
	store.order.Status = "fulfilled"
	h := adminFulfillHandler(t, store)
	rec, body := callJSONBody(t, h, http.MethodPost, "/v1/admin/orders/order-1/fulfill", "jwt-owner", `{}`, nil)
	if rec.Code != http.StatusOK || body["already_fulfilled"] != true {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if len(store.calls) != 0 {
		t.Fatalf("second fulfill applied credits: %v", store.calls)
	}
}

func TestAdminFulfillOrderRejectsRefunded(t *testing.T) {
	store := &fakeOrderAdmin{order: pendingOrder()}
	store.order.Status = "refunded"
	h := adminFulfillHandler(t, store)
	rec, _ := callJSONBody(t, h, http.MethodPost, "/v1/admin/orders/order-1/fulfill", "jwt-owner", `{}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", rec.Code)
	}
}

func TestAdminFulfillOrderRequiresAdmin(t *testing.T) {
	store := &fakeOrderAdmin{order: pendingOrder()}
	h := adminFulfillHandler(t, store)
	rec, _ := callJSONBody(t, h, http.MethodPost, "/v1/admin/orders/order-1/fulfill", "jwt-other", `{}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rec.Code)
	}
	if len(store.calls) != 0 {
		t.Fatal("non-admin mutated the order")
	}
}
