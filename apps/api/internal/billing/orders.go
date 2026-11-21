package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type CreditPack struct {
	SKU         string
	AmountCents int
	Credits     int64
	Title       string
}

// AlwaysOnSKU buys the always-on sandbox add-on. It carries no credits: its
// effect is the billing.addons expiry that /api/auth/validate reports as the
// workspace:always_on scope.
const AlwaysOnSKU = "addon_always_on_30d"

var CreditPacks = map[string]CreditPack{
	"credit_50":  {"credit_50", 5000, 5000, "Roost 5,000 credits"},
	"credit_100": {"credit_100", 10000, 10000, "Roost 10,000 credits"},
	"credit_500": {"credit_500", 50000, 50000, "Roost 50,000 credits"},
	// Token packs are the same credit balance, sold under a token-first name.
	"token_50":  {"token_50", 5000, 5000, "Roost 5,000 token credits"},
	"token_100": {"token_100", 10000, 10000, "Roost 10,000 token credits"},
	"token_500": {"token_500", 50000, 50000, "Roost 50,000 token credits"},
	AlwaysOnSKU: {AlwaysOnSKU, 4900, 0, "Roost Always-on · 30 days"},
}

func randomTradeNo() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

// Quote returns the server-side price and credits for a SKU. Client input never
// supplies either value.
func (s *Store) Quote(ctx context.Context, sku string) (CreditPack, string, error) {
	if pack, ok := CreditPacks[sku]; ok {
		return pack, "credits", nil
	}
	if strings.HasPrefix(sku, "plan_") && strings.HasSuffix(sku, "_30d") {
		code := strings.TrimSuffix(strings.TrimPrefix(sku, "plan_"), "_30d")
		plan, err := s.Plan(ctx, code)
		if err != nil {
			return CreditPack{}, "", err
		}
		return CreditPack{SKU: sku, AmountCents: plan.PriceCents, Credits: plan.IncludedCredits, Title: "Roost " + plan.Name + " · 30 days"}, "plan:" + code, nil
	}
	return CreditPack{}, "", ErrNotFound
}

// Custom top-up: the user chooses the amount, and credits are 1:1 with cents
// (100 credits = ¥1). The price is always derived server-side from the amount,
// so a client cannot buy a different number of credits than it pays for.
const (
	CustomTopUpSKU      = "credit_custom"
	CustomTopUpMinCents = 1000   // ¥10
	CustomTopUpMaxCents = 500000 // ¥5000
)

var ErrInvalidTopUpAmount = errors.New("invalid top-up amount")

// CustomTopUpQuote validates a custom amount and returns the pack it buys.
func CustomTopUpQuote(amountCents int64) (CreditPack, error) {
	if amountCents < CustomTopUpMinCents || amountCents > CustomTopUpMaxCents {
		return CreditPack{}, ErrInvalidTopUpAmount
	}
	return CreditPack{
		SKU:         CustomTopUpSKU,
		AmountCents: int(amountCents),
		Credits:     amountCents,
		Title:       "Roost 自定义充值",
	}, nil
}

// CreateCustomOrder persists a custom top-up before the provider checkout is
// created, matching CreateOrder's lifecycle (created -> pending -> fulfilled).
func (s *Store) CreateCustomOrder(ctx context.Context, orgID string, amountCents int64) (Order, error) {
	pack, err := CustomTopUpQuote(amountCents)
	if err != nil {
		return Order{}, err
	}
	var o Order
	err = s.Pool.QueryRow(ctx, `
		insert into billing.orders (org_id,out_trade_no,sku,amount_cents,credits,status)
		values ($1,$2,$3,$4,$5,'created')
		returning id,org_id,out_trade_no,sku,amount_cents,credits,status,created_at`,
		orgID, randomTradeNo(), pack.SKU, pack.AmountCents, pack.Credits,
	).Scan(&o.ID, &o.OrgID, &o.OutTradeNo, &o.SKU, &o.AmountCents, &o.Credits, &o.Status, &o.CreatedAt)
	return o, err
}

func (s *Store) CreateOrder(ctx context.Context, orgID, sku string) (Order, error) {
	pack, kind, err := s.Quote(ctx, sku)
	if err != nil {
		return Order{}, err
	}
	if sku == AlwaysOnSKU {
		// The add-on is a Pro/Max optional, never a Starter one. Read the plan
		// through the same lazy-expiry path Billing uses so an elapsed paid
		// period cannot buy it.
		var planCode string
		if err := s.Pool.QueryRow(ctx, `select plan_code from public.organizations where id=$1`, orgID).Scan(&planCode); err != nil {
			return Order{}, err
		}
		effective, err := s.currentPlan(ctx, orgID, planCode)
		if err != nil {
			return Order{}, err
		}
		if effective != "pro" && effective != "max" {
			return Order{}, ErrAddonNotEligible
		}
	}
	var o Order
	err = s.Pool.QueryRow(ctx, `
		insert into billing.orders (org_id,out_trade_no,sku,amount_cents,credits,status)
		values ($1,$2,$3,$4,$5,'created')
		returning id,org_id,out_trade_no,sku,amount_cents,credits,status,created_at`,
		orgID, randomTradeNo(), sku, pack.AmountCents, pack.Credits,
	).Scan(&o.ID, &o.OrgID, &o.OutTradeNo, &o.SKU, &o.AmountCents, &o.Credits, &o.Status, &o.CreatedAt)
	_ = kind
	return o, err
}

func (s *Store) AttachPayment(ctx context.Context, orderID, providerOrderID, qrURL, payURL string) error {
	_, err := s.Pool.Exec(ctx, `update billing.orders set status='pending',provider_order_id=$2,qr_url=$3,pay_url=$4,updated_at=now() where id=$1 and status in ('created','pending')`, orderID, providerOrderID, qrURL, payURL)
	return err
}

func (s *Store) GetOrder(ctx context.Context, orgID, orderID string) (Order, error) {
	var o Order
	err := s.Pool.QueryRow(ctx, `
		select id,org_id,out_trade_no,sku,amount_cents,credits,status,coalesce(qr_url,''),coalesce(pay_url,''),paid_at,created_at
		from billing.orders where id=$1 and org_id=$2`, orderID, orgID).
		Scan(&o.ID, &o.OrgID, &o.OutTradeNo, &o.SKU, &o.AmountCents, &o.Credits, &o.Status, &o.QRURL, &o.PayURL, &o.PaidAt, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	return o, err
}

func (s *Store) OrderByTradeNo(ctx context.Context, tradeNo string) (Order, error) {
	var o Order
	err := s.Pool.QueryRow(ctx, `
		select id,org_id,out_trade_no,sku,amount_cents,credits,status,coalesce(qr_url,''),coalesce(pay_url,''),paid_at,created_at
		from billing.orders where out_trade_no=$1`, tradeNo).
		Scan(&o.ID, &o.OrgID, &o.OutTradeNo, &o.SKU, &o.AmountCents, &o.Credits, &o.Status, &o.QRURL, &o.PayURL, &o.PaidAt, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	return o, err
}

// Fulfill marks an order paid and applies its effect exactly once. amountCents
// is the amount the provider says was paid; callers must verify it first.
func (s *Store) Fulfill(ctx context.Context, orderID, providerTradeNo string, amountCents int, raw []byte) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var o Order
	err = tx.QueryRow(ctx, `select id,org_id,sku,amount_cents,credits,status from billing.orders where id=$1 for update`, orderID).
		Scan(&o.ID, &o.OrgID, &o.SKU, &o.AmountCents, &o.Credits, &o.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if amountCents != o.AmountCents {
		return fmt.Errorf("payment amount mismatch: got %d want %d", amountCents, o.AmountCents)
	}
	if o.Status == "fulfilled" {
		return nil
	}
	if o.Status == "refunded" {
		return fmt.Errorf("order refunded")
	}
	rawJSON := string(raw)
	if o.SKU == AlwaysOnSKU {
		// Stack on the current expiry so early renewals never lose days; the
		// expiry lives on this order's raw payload.
		var base time.Time
		if err := tx.QueryRow(ctx, `
			select greatest(coalesce(max((raw->>'addon_until')::timestamptz), now()), now())
			from billing.orders
			where org_id=$1 and sku=$2 and status in ('paid','fulfilled') and raw ? 'addon_until'`,
			o.OrgID, AlwaysOnSKU).Scan(&base); err != nil {
			return err
		}
		until := base.Add(30 * 24 * time.Hour)
		payload := map[string]any{}
		_ = json.Unmarshal(raw, &payload)
		payload["addon_until"] = until.UTC().Format(time.RFC3339)
		merged, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		rawJSON = string(merged)
	}
	if _, err := tx.Exec(ctx, `
		update billing.orders set status='paid',provider_trade_no=$2,paid_at=now(),raw=$3,updated_at=now()
		where id=$1`, o.ID, providerTradeNo, rawJSON); err != nil {
		return err
	}
	if o.Credits > 0 {
		if _, err := tx.Exec(ctx, `
			insert into billing.ledger (org_id,delta_credits,reason,ref_id,idempotency_key)
			values ($1,$2,'payment',$3,$4)
			on conflict (idempotency_key) do nothing`, o.OrgID, o.Credits, o.ID, "order:"+o.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update billing.wallets set balance_credits=balance_credits+$2,version=version+1,updated_at=now()
			where org_id=$1`, o.OrgID, o.Credits); err != nil {
			return err
		}
	}
	if strings.HasPrefix(o.SKU, "plan_") {
		code := strings.TrimSuffix(strings.TrimPrefix(o.SKU, "plan_"), "_30d")
		var duration int
		if err := tx.QueryRow(ctx, `select duration_days from billing.plans where code=$1`, code).Scan(&duration); err != nil {
			return err
		}
		if duration <= 0 {
			duration = 30
		}
		if _, err := tx.Exec(ctx, `
			insert into billing.subscriptions (org_id,plan_code,status,period_start,period_end,source_order_id)
			values ($1,$2,'active',now(),now()+make_interval(days=>$3),$4)
			on conflict (org_id) do update set
				plan_code=excluded.plan_code,
				status='active',
				period_start=now(),
				period_end=greatest(billing.subscriptions.period_end, now()) + make_interval(days=>$3),
				source_order_id=excluded.source_order_id,
				updated_at=now()`, o.OrgID, code, duration, o.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update public.organizations set plan_code=$2,updated_at=now() where id=$1`, o.OrgID, code); err != nil {
			return err
		}
		if err := s.applyEntitlements(ctx, tx, o.OrgID, code); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update billing.orders set status='fulfilled',fulfilled_at=now(),updated_at=now() where id=$1`, o.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListOrders(ctx context.Context, orgID string, limit int) ([]Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.Pool.Query(ctx, `
		select id,org_id,out_trade_no,sku,amount_cents,credits,status,coalesce(qr_url,''),coalesce(pay_url,''),paid_at,created_at
		from billing.orders where org_id=$1 order by created_at desc limit $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Order, 0)
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.OrgID, &o.OutTradeNo, &o.SKU, &o.AmountCents, &o.Credits, &o.Status, &o.QRURL, &o.PayURL, &o.PaidAt, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func nowPtr() *time.Time { t := time.Now(); return &t }

// MarkOrderFailed closes an order whose provider checkout could not be created.
// It only touches a still-'created' row: a race with a notify must never flip a
// paid order to failed.
func (s *Store) MarkOrderFailed(ctx context.Context, orderID, reason string) error {
	_, err := s.Pool.Exec(ctx, `
		update billing.orders
		set status='failed', raw = raw || jsonb_build_object('failure', $2::text), updated_at=now()
		where id=$1 and status='created'`, orderID, reason)
	return err
}

// ExpireStaleOrders closes abandoned checkouts: an order that stayed
// created/pending past the TTL can never be paid, so it should stop reading as
// "in progress". A later provider notify still fulfills it (money first).
func (s *Store) ExpireStaleOrders(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		olderThan = 2 * time.Hour
	}
	tag, err := s.Pool.Exec(ctx, `
		update billing.orders
		set status='expired', updated_at=now()
		where status in ('created','pending')
		  and created_at < now() - make_interval(secs => $1)`, olderThan.Seconds())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListReconcileOrders returns pending orders old enough that a notify should
// already have arrived, newest window first. The reconciler queries the
// provider for each one.
func (s *Store) ListReconcileOrders(ctx context.Context, minAge time.Duration, limit int) ([]Order, error) {
	if minAge <= 0 {
		minAge = time.Minute
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		select id,org_id,out_trade_no,sku,amount_cents,credits,status,coalesce(qr_url,''),coalesce(pay_url,''),paid_at,created_at
		from billing.orders
		where status='pending'
		  and created_at < now() - make_interval(secs => $1)
		  and created_at > now() - interval '24 hours'
		  and (raw->>'reconcile_at' is null
		       or (raw->>'reconcile_at')::timestamptz < now() - interval '10 minutes')
		order by created_at
		limit $2`, minAge.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Order, 0, limit)
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.OrgID, &o.OutTradeNo, &o.SKU, &o.AmountCents, &o.Credits, &o.Status, &o.QRURL, &o.PayURL, &o.PaidAt, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// TouchOrderReconcile records when the reconciler last queried the provider so
// an unpaid order is not re-queried every tick.
func (s *Store) TouchOrderReconcile(ctx context.Context, orderID string) error {
	_, err := s.Pool.Exec(ctx, `
		update billing.orders
		set raw = raw || jsonb_build_object('reconcile_at', now()), updated_at=now()
		where id=$1 and status='pending'`, orderID)
	return err
}

// CloseOrder marks one order with a terminal non-paid status ('expired' for a
// provider-cancelled checkout). Paid orders are never touched.
func (s *Store) CloseOrder(ctx context.Context, orderID, status string) error {
	switch status {
	case "expired", "failed":
	default:
		return fmt.Errorf("close order: unsupported status %q", status)
	}
	_, err := s.Pool.Exec(ctx, `
		update billing.orders set status=$2, updated_at=now()
		where id=$1 and status in ('created','pending')`, orderID, status)
	return err
}

// GetOrderByID loads an order without an org scope; admin-only callers use it.
func (s *Store) GetOrderByID(ctx context.Context, orderID string) (Order, error) {
	var o Order
	err := s.Pool.QueryRow(ctx, `
		select id,org_id,out_trade_no,sku,amount_cents,credits,status,coalesce(qr_url,''),coalesce(pay_url,''),paid_at,created_at
		from billing.orders where id=$1`, orderID).
		Scan(&o.ID, &o.OrgID, &o.OutTradeNo, &o.SKU, &o.AmountCents, &o.Credits, &o.Status, &o.QRURL, &o.PayURL, &o.PaidAt, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	return o, err
}
