package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Relay billing surface. The relay forwards model traffic and keeps its own
// settlement state; the console is the money ledger. Reservations reuse
// billing.usage_leases (run_ref is the relay request id) and settlements reuse
// billing.ledger (the relay's idempotency key), so this needs no new tables.
const (
	RelayProduct  = "relay"
	RelayCurrency = "CREDIT"
)

var (
	ErrRelayConflict = errors.New("relay: idempotency conflict")
	ErrRelayNotFound = errors.New("relay: not found")
	ErrRelayReleased = errors.New("relay: reservation already released")
)

type RelayReservation struct {
	ID        string
	Amount    int64
	ExpiresAt time.Time
}

type RelayOperation struct {
	Found         bool
	Operation     string
	Status        string
	ReservationID string
	Amount        int64
	ErrorCode     string
}

// RelayAPIKey resolves an rk- key to its organization.
func (s *Store) RelayAPIKey(ctx context.Context, token string) (string, error) {
	hash := hashProductToken(token)
	var orgID string
	err := s.Pool.QueryRow(ctx, `
		select org_id from billing.product_tokens
		where token_hash=$1 and product='relay' and revoked_at is null`, hash).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRelayNotFound
	}
	if err != nil {
		return "", err
	}
	_, _ = s.Pool.Exec(ctx, `update billing.product_tokens set last_used_at=now() where token_hash=$1`, hash)
	return orgID, nil
}

// RelayBalance returns the organization's available credits (balance - holds).
func (s *Store) RelayBalance(ctx context.Context, orgID string) (int64, error) {
	var balance, reserved int64
	err := s.Pool.QueryRow(ctx, `select balance_credits,reserved_credits from billing.wallets where org_id=$1`, orgID).Scan(&balance, &reserved)
	if err != nil {
		return 0, err
	}
	return balance - reserved, nil
}

// RelayReserve holds the request's estimated spend. Replaying the same request
// returns the first hold; a different amount on the same run_ref is a conflict.
//
// It deliberately avoids an explicit transaction and advisory locks: the lease
// row is the idempotency record (unique org+product+run_ref) and one CTE both
// reserves the wallet and inserts the lease atomically. This keeps the
// pre-forwarding hop to two database round trips, which matters because this
// call sits on the request's first-token latency path.
func (s *Store) RelayReserve(ctx context.Context, orgID, requestID string, amount int64, currency string, ttl time.Duration) (RelayReservation, error) {
	if amount < 0 {
		return RelayReservation{}, errors.New("relay: reserve amount must be >= 0")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	// One statement for the common path: atomically reserve the wallet and
	// insert the lease. Replays and races hit the unique key and fall back to a
	// read below, so the request's first-token path pays one database round trip.
	var out RelayReservation
	var updated int64
	err := s.Pool.QueryRow(ctx, `
		with upd as (
			update billing.wallets
			set reserved_credits=reserved_credits+$3, version=version+1, updated_at=now()
			where org_id=$1 and balance_credits-reserved_credits >= $3
			returning balance_credits
		), ins as (
			insert into billing.usage_leases (org_id,product,run_ref,reserved_credits,status,expires_at)
			select $1,'relay',$2,$3,'active',now()+make_interval(secs=>$4)
			where exists (select 1 from upd)
			returning id,reserved_credits,expires_at
		)
		select id,reserved_credits,expires_at,(select count(*) from upd) from ins`,
		orgID, requestID, amount, int(ttl.Seconds())).
		Scan(&out.ID, &out.Amount, &out.ExpiresAt, &updated)
	if err == nil {
		return out, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// Replay or a concurrent reserve won the unique key; return that hold.
		var existing RelayReservation
		var status string
		if qerr := s.Pool.QueryRow(ctx, `
			select id,reserved_credits,status,expires_at from billing.usage_leases
			where org_id=$1 and product='relay' and run_ref=$2`, orgID, requestID).
			Scan(&existing.ID, &existing.Amount, &status, &existing.ExpiresAt); qerr == nil {
			if existing.Amount != amount || status != "active" {
				return RelayReservation{}, ErrRelayConflict
			}
			return existing, nil
		}
	}
	if errors.Is(err, pgx.ErrNoRows) || updated == 0 {
		// Either the wallet does not exist or the conditional update found no
		// free balance; both are an insufficient-balance rejection.
		return RelayReservation{}, ErrInsufficientCredits
	}
	return RelayReservation{}, err
}

// RelaySettle charges the reservation's actual spend exactly once, keyed by the
// relay's settle idempotency key. The settle call names only the reservation, so
// the organization is read from it.
func (s *Store) RelaySettle(ctx context.Context, reservationID, idempotencyKey string, amount int64) (RelayOperation, error) {
	if amount < 0 {
		return RelayOperation{}, errors.New("relay: settle amount must be >= 0")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return RelayOperation{}, err
	}
	defer tx.Rollback(ctx)
	// Replay of an already-booked settlement.
	var booked int64
	var refID string
	if err := tx.QueryRow(ctx, `select delta_credits,coalesce(ref_id,'') from billing.ledger where idempotency_key=$1`, idempotencyKey).Scan(&booked, &refID); err == nil {
		return RelayOperation{Found: true, Operation: "settle", Status: "settled", ReservationID: refID, Amount: -booked}, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RelayOperation{}, err
	}
	var orgID string
	var reserved int64
	var status string
	if err := tx.QueryRow(ctx, `
		select org_id,reserved_credits,status from billing.usage_leases
		where id=$1 and product='relay' for update`, reservationID).Scan(&orgID, &reserved, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RelayOperation{}, ErrRelayNotFound
		}
		return RelayOperation{}, err
	}
	if status == "released" || status == "expired" {
		return RelayOperation{}, ErrRelayReleased
	}
	if status == "settled" {
		var settled int64
		_ = tx.QueryRow(ctx, `select coalesce(settled_credits,0) from billing.usage_leases where id=$1`, reservationID).Scan(&settled)
		return RelayOperation{Found: true, Operation: "settle", Status: "settled", ReservationID: reservationID, Amount: settled}, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "wallet:"+orgID); err != nil {
		return RelayOperation{}, err
	}
	var balance int64
	if err := tx.QueryRow(ctx, `select balance_credits from billing.wallets where org_id=$1 for update`, orgID).Scan(&balance); err != nil {
		return RelayOperation{}, err
	}
	extra := amount - reserved
	if extra > 0 && balance-reserved < extra {
		return RelayOperation{}, ErrInsufficientCredits
	}
	if _, err := tx.Exec(ctx, `
		update billing.wallets set balance_credits=greatest(0,balance_credits-$2),
			reserved_credits=greatest(0,reserved_credits-$3),version=version+1,updated_at=now()
		where org_id=$1`, orgID, amount, reserved); err != nil {
		return RelayOperation{}, err
	}
	if _, err := tx.Exec(ctx, `
		insert into billing.ledger (org_id,product,delta_credits,reason,ref_id,idempotency_key)
		values ($1,'relay',$2,'relay_usage',$3,$4)
		on conflict (idempotency_key) do nothing`, orgID, -amount, reservationID, idempotencyKey); err != nil {
		return RelayOperation{}, err
	}
	if _, err := tx.Exec(ctx, `update billing.usage_leases set status='settled',settled_credits=$2 where id=$1`, reservationID, amount); err != nil {
		return RelayOperation{}, err
	}
	return RelayOperation{Found: true, Operation: "settle", Status: "settled", ReservationID: reservationID, Amount: amount}, tx.Commit(ctx)
}

// RelayRelease frees a hold that never produced billable upstream work.
func (s *Store) RelayRelease(ctx context.Context, reservationID string) (RelayOperation, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return RelayOperation{}, err
	}
	defer tx.Rollback(ctx)
	var orgID string
	var reserved int64
	var status string
	if err := tx.QueryRow(ctx, `
		select org_id,reserved_credits,status from billing.usage_leases
		where id=$1 and product='relay' for update`, reservationID).Scan(&orgID, &reserved, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RelayOperation{}, ErrRelayNotFound
		}
		return RelayOperation{}, err
	}
	if status == "released" {
		return RelayOperation{Found: true, Operation: "release", Status: "released", ReservationID: reservationID}, tx.Commit(ctx)
	}
	if status == "settled" {
		return RelayOperation{}, ErrRelayConflict
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "wallet:"+orgID); err != nil {
		return RelayOperation{}, err
	}
	if _, err := tx.Exec(ctx, `update billing.wallets set reserved_credits=greatest(0,reserved_credits-$2),version=version+1,updated_at=now() where org_id=$1`, orgID, reserved); err != nil {
		return RelayOperation{}, err
	}
	if _, err := tx.Exec(ctx, `update billing.usage_leases set status='released' where id=$1`, reservationID); err != nil {
		return RelayOperation{}, err
	}
	return RelayOperation{Found: true, Operation: "release", Status: "released", ReservationID: reservationID}, tx.Commit(ctx)
}

// RelayOperation finds the first result of an idempotency key. The relay uses
// this before retrying any operation whose result was unknown.
func (s *Store) RelayOperation(ctx context.Context, idempotencyKey string) (RelayOperation, error) {
	op, requestID, ok := strings.Cut(idempotencyKey, ":")
	if !ok || requestID == "" {
		return RelayOperation{Found: false}, nil
	}
	switch op {
	case "reserve", "release":
		var id string
		var reserved, settled int64
		var status string
		err := s.Pool.QueryRow(ctx, `
			select id,reserved_credits,coalesce(settled_credits,0),status from billing.usage_leases
			where product='relay' and run_ref=$1 order by created_at desc limit 1`, requestID).
			Scan(&id, &reserved, &settled, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return RelayOperation{Found: false}, nil
		}
		if err != nil {
			return RelayOperation{}, err
		}
		amount := reserved
		if op == "release" && status != "released" {
			// The key is a release key but no release happened yet.
			return RelayOperation{Found: false}, nil
		}
		if op == "reserve" && status == "settled" {
			amount = settled
		}
		return RelayOperation{Found: true, Operation: op, Status: status, ReservationID: id, Amount: amount}, nil
	case "settle":
		var delta int64
		var refID string
		err := s.Pool.QueryRow(ctx, `select delta_credits,coalesce(ref_id,'') from billing.ledger where idempotency_key=$1`, idempotencyKey).Scan(&delta, &refID)
		if errors.Is(err, pgx.ErrNoRows) {
			return RelayOperation{Found: false}, nil
		}
		if err != nil {
			return RelayOperation{}, err
		}
		return RelayOperation{Found: true, Operation: "settle", Status: "settled", ReservationID: refID, Amount: -delta}, nil
	default:
		return RelayOperation{Found: false}, nil
	}
}
