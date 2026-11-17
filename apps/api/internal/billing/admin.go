package billing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// AdminOrg is the operations view of one organization.
type AdminOrg struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	PlanCode      string `json:"plan_code"`
	OwnerUserID   string `json:"owner_user_id"`
	BalanceCredit int64  `json:"balance_credits"`
	Reserved      int64  `json:"reserved_credits"`
	CreatedAt     string `json:"created_at"`
}

const adminOrgSelect = `
	select o.id, o.name, o.slug, o.plan_code, o.owner_user_id,
	       coalesce(w.balance_credits,0), coalesce(w.reserved_credits,0), o.created_at::text
	from public.organizations o
	left join billing.wallets w on w.org_id = o.id`

func scanAdminOrg(row pgx.Row) (AdminOrg, error) {
	var a AdminOrg
	err := row.Scan(&a.ID, &a.Name, &a.Slug, &a.PlanCode, &a.OwnerUserID, &a.BalanceCredit, &a.Reserved, &a.CreatedAt)
	return a, err
}

// AdminSearchOrgs finds organizations by id, slug or name fragment.
func (s *Store) AdminSearchOrgs(ctx context.Context, query string) ([]AdminOrg, error) {
	pattern := "%" + query + "%"
	rows, err := s.Pool.Query(ctx, adminOrgSelect+`
		where o.id::text = $1 or o.slug ilike $2 or o.name ilike $2
		order by o.created_at desc limit 50`, query, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AdminOrg, 0)
	for rows.Next() {
		a, err := scanAdminOrg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AdminOrg loads one organization for the operations view.
func (s *Store) AdminOrg(ctx context.Context, orgID string) (AdminOrg, error) {
	a, err := scanAdminOrg(s.Pool.QueryRow(ctx, adminOrgSelect+` where o.id=$1`, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminOrg{}, ErrNotFound
	}
	return a, err
}

// AdminAdjustCredits books an operator adjustment exactly once. The ledger row
// is the audit record; the wallet is never written without one.
func (s *Store) AdminAdjustCredits(ctx context.Context, orgID string, delta int64, reason, idempotencyKey, operator string) (int64, error) {
	if delta == 0 {
		return 0, errors.New("admin: delta must not be zero")
	}
	if idempotencyKey == "" {
		return 0, errors.New("admin: idempotency key is required")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "wallet:"+orgID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		insert into billing.ledger (org_id,delta_credits,reason,ref_id,idempotency_key)
		values ($1,$2,'admin_adjust',$3,$4)
		on conflict (idempotency_key) do nothing`, orgID, delta, operator, idempotencyKey); err != nil {
		return 0, err
	}
	var balance int64
	if err := tx.QueryRow(ctx, `
		update billing.wallets set balance_credits=greatest(0,balance_credits+$2),version=version+1,updated_at=now()
		where org_id=$1 returning balance_credits`, orgID, delta).Scan(&balance); err != nil {
		return 0, err
	}
	return balance, tx.Commit(ctx)
}

// AdminLedger lists recent ledger rows for one organization.
func (s *Store) AdminLedger(ctx context.Context, orgID string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		select coalesce(product,''), delta_credits, reason, coalesce(ref_id,''), created_at::text
		from billing.ledger where org_id=$1 order by created_at desc limit $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var product, reason, ref, created string
		var delta int64
		if err := rows.Scan(&product, &delta, &reason, &ref, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"product": product, "delta_credits": delta, "reason": reason, "ref_id": ref, "created_at": created})
	}
	return out, rows.Err()
}
