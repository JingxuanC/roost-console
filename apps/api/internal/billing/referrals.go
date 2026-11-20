package billing

import (
	"context"
	"crypto/rand"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Referral program. Codes are per organization, rewards are booked in
// billing.ledger in the same transaction as the billing.referrals row, and an
// invitee can be rewarded at most once.

const (
	// ReferralAccountAgeWindow bounds the referral program to new accounts. A
	// user who signed up more than seven days ago is not eligible to claim.
	ReferralAccountAgeWindow = 7 * 24 * time.Hour

	ReferralStatusRewarded = "rewarded"

	referralCodeLength = 10
	// Excludes visually ambiguous I/O/0/1 so a code can be read aloud.
	referralCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var (
	ErrReferralCodeNotFound   = errors.New("referral code not found")
	ErrReferralCodeInactive   = errors.New("referral code is inactive")
	ErrReferralCodeExhausted  = errors.New("referral code has reached its maximum uses")
	ErrReferralSelfClaim      = errors.New("you cannot claim your own referral code")
	ErrReferralNotEligible    = errors.New("this account is not eligible for a referral reward")
	ErrReferralAccountTooOld  = errors.New("referral rewards are limited to accounts created in the last 7 days")
	ErrReferralInviteeMissing = errors.New("referral invitee account not found")
)

// ReferralOverview is the owner-facing invite summary. Reward amounts live in
// configuration, not in the code row, so the console can change them without a
// migration.
type ReferralOverview struct {
	Code                string `json:"code"`
	InvitedCount        int64  `json:"invited_count"`
	TotalInviterCredits int64  `json:"total_inviter_credits"`
	TotalInviteeCredits int64  `json:"total_invitee_credits"`
}

// ClaimReferralInput is one referral claim. The caller passes the reward
// amounts from configuration; the store persists them so a later configuration
// change cannot rewrite history.
type ClaimReferralInput struct {
	Code             string
	InviteeUserID    string
	InviteeOrgID     string
	InviterCredits   int64
	InviteeCredits   int64
	AccountCreatedAt time.Time // Supabase auth fallback when auth.users is unreadable
	Now              time.Time
}

// ClaimReferralResult is the replay-safe outcome of a claim.
type ClaimReferralResult struct {
	ReferralID            string     `json:"referral_id"`
	Code                  string     `json:"code"`
	Status                string     `json:"status"`
	AlreadyClaimed        bool       `json:"already_claimed"`
	InviterCredits        int64      `json:"inviter_credits"`
	InviteeCredits        int64      `json:"invitee_credits"`
	BalanceCredits        int64      `json:"balance_credits"`
	InviterBalanceCredits int64      `json:"-"`
	RewardedAt            *time.Time `json:"rewarded_at,omitempty"`
}

// AdminReferralRow is one referral in the operations view.
type AdminReferralRow struct {
	ID             string     `json:"id"`
	Code           string     `json:"code"`
	InviterUserID  string     `json:"inviter_user_id"`
	InviterOrgID   string     `json:"inviter_org_id"`
	InviterOrgName string     `json:"inviter_org_name"`
	InviteeUserID  string     `json:"invitee_user_id"`
	InviteeOrgID   string     `json:"invitee_org_id"`
	InviteeOrgName string     `json:"invitee_org_name"`
	Status         string     `json:"status"`
	InviterCredits int64      `json:"inviter_credits"`
	InviteeCredits int64      `json:"invitee_credits"`
	CreatedAt      time.Time  `json:"created_at"`
	RewardedAt     *time.Time `json:"rewarded_at"`
}

// AdminReferrals is the platform referral summary plus the newest rows.
type AdminReferrals struct {
	TotalReferrals      int64              `json:"total_referrals"`
	RewardedReferrals   int64              `json:"rewarded_referrals"`
	TotalInviterCredits int64              `json:"total_inviter_credits"`
	TotalInviteeCredits int64              `json:"total_invitee_credits"`
	Referrals           []AdminReferralRow `json:"referrals"`
}

// NormalizeReferralCode trims and uppercases a user-supplied code. Codes are
// stored uppercase; accepting lowercase input avoids a pointless claim failure.
func NormalizeReferralCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// newReferralCode returns a random uppercase URL-safe code. The alphabet omits
// I/O/0/1 so the code is unambiguous when copied by hand.
func newReferralCode() (string, error) {
	buf := make([]byte, referralCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = referralCodeAlphabet[int(buf[i])%len(referralCodeAlphabet)]
	}
	return string(buf), nil
}

// ReferralOverview ensures the organization owns a code and returns its invite
// statistics. The write is idempotent: one code per organization, enforced by
// the unique owner index and a per-organization advisory lock.
func (s *Store) ReferralOverview(ctx context.Context, userID, orgID string, maxUses int) (ReferralOverview, error) {
	code, err := s.ensureReferralCode(ctx, userID, orgID, maxUses)
	if err != nil {
		return ReferralOverview{}, err
	}
	out := ReferralOverview{Code: code}
	err = s.Pool.QueryRow(ctx, `
		select count(*)::bigint,
		       coalesce(sum(inviter_credits),0)::bigint,
		       coalesce(sum(invitee_credits),0)::bigint
		from billing.referrals
		where inviter_org_id=$1 and status='rewarded'`, orgID).
		Scan(&out.InvitedCount, &out.TotalInviterCredits, &out.TotalInviteeCredits)
	return out, err
}

func (s *Store) ensureReferralCode(ctx context.Context, userID, orgID string, maxUses int) (string, error) {
	if userID == "" || orgID == "" {
		return "", ErrReferralNotEligible
	}
	if maxUses < 0 {
		maxUses = 0
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "referral-code:"+orgID); err != nil {
		return "", err
	}
	var code string
	err = tx.QueryRow(ctx, `select code from billing.referral_codes where owner_org_id=$1`, orgID).Scan(&code)
	if err == nil {
		return code, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}

	// A random collision is vanishingly unlikely, but retry rather than fail a
	// first-time invite. `on conflict do nothing` also handles a concurrent
	// request that created this organization's code while we waited on the lock.
	for attempt := 0; attempt < 8; attempt++ {
		candidate, genErr := newReferralCode()
		if genErr != nil {
			return "", genErr
		}
		tag, execErr := tx.Exec(ctx, `
			insert into billing.referral_codes (code,owner_user_id,owner_org_id,max_uses)
			values ($1,$2,$3,$4)
			on conflict do nothing`, candidate, userID, orgID, maxUses)
		if execErr != nil {
			return "", execErr
		}
		if tag.RowsAffected() == 1 {
			return candidate, tx.Commit(ctx)
		}
		if err := tx.QueryRow(ctx, `select code from billing.referral_codes where owner_org_id=$1`, orgID).Scan(&code); err == nil {
			return code, tx.Commit(ctx)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	return "", errors.New("referral: could not allocate a unique code")
}

// ClaimReferral rewards one invitee exactly once. The transaction takes a
// per-invitee advisory lock first (serializing duplicate claims), then a
// per-code lock (protecting uses/max_uses), inserts the unique referral row and
// only then books both ledger rows and wallet updates. A replay returns the
// existing referral without a second credit.
func (s *Store) ClaimReferral(ctx context.Context, in ClaimReferralInput) (ClaimReferralResult, error) {
	code := NormalizeReferralCode(in.Code)
	if code == "" {
		return ClaimReferralResult{}, ErrReferralCodeNotFound
	}
	if in.InviteeUserID == "" || in.InviteeOrgID == "" {
		return ClaimReferralResult{}, ErrReferralNotEligible
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	inviterCredits := maxInt64(in.InviterCredits, 0)
	inviteeCredits := maxInt64(in.InviteeCredits, 0)

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ClaimReferralResult{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "referral-invitee:"+in.InviteeUserID); err != nil {
		return ClaimReferralResult{}, err
	}
	// Idempotency: an invitee who already claimed any code gets the stored
	// referral back, whatever code they submitted this time.
	if existing, found, err := existingReferralTx(ctx, tx, in.InviteeUserID); err != nil {
		return ClaimReferralResult{}, err
	} else if found {
		return existing, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "referral-claim:"+code); err != nil {
		return ClaimReferralResult{}, err
	}

	var (
		ownerUserID, ownerOrgID string
		active                  bool
		uses, maxUses           int
	)
	err = tx.QueryRow(ctx, `
		select owner_user_id::text, owner_org_id::text, active, uses, max_uses
		from billing.referral_codes
		where code=$1
		for update`, code).
		Scan(&ownerUserID, &ownerOrgID, &active, &uses, &maxUses)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimReferralResult{}, ErrReferralCodeNotFound
	}
	if err != nil {
		return ClaimReferralResult{}, err
	}

	accountCreated, err := referralAccountCreatedAt(ctx, tx, in.InviteeUserID)
	if err != nil {
		// The API role normally reads auth.users. If the deployment revokes that
		// grant, the Supabase-verified created_at is the same authoritative value.
		if in.AccountCreatedAt.IsZero() {
			if errors.Is(err, pgx.ErrNoRows) {
				return ClaimReferralResult{}, ErrReferralInviteeMissing
			}
			return ClaimReferralResult{}, err
		}
		accountCreated = in.AccountCreatedAt
	}
	if accountCreated.IsZero() {
		accountCreated = in.AccountCreatedAt
	}

	claim := referralClaim{
		CodeActive:       active,
		Uses:             uses,
		MaxUses:          maxUses,
		InviterUserID:    ownerUserID,
		InviterOrgID:     ownerOrgID,
		InviteeUserID:    in.InviteeUserID,
		InviteeOrgID:     in.InviteeOrgID,
		AccountCreatedAt: accountCreated,
	}
	if err := validateReferralClaim(claim, now, ReferralAccountAgeWindow); err != nil {
		return ClaimReferralResult{}, err
	}

	if err := lockReferralWallets(ctx, tx, ownerOrgID, in.InviteeOrgID); err != nil {
		return ClaimReferralResult{}, err
	}

	var (
		referralID string
		rewardedAt time.Time
	)
	err = tx.QueryRow(ctx, `
		insert into billing.referrals
			(code,inviter_user_id,inviter_org_id,invitee_user_id,invitee_org_id,
			 status,inviter_credits,invitee_credits,rewarded_at)
		values ($1,$2,$3,$4,$5,'rewarded',$6,$7,now())
		on conflict (invitee_user_id) do nothing
		returning id::text, rewarded_at`,
		code, ownerUserID, ownerOrgID, in.InviteeUserID, in.InviteeOrgID,
		inviterCredits, inviteeCredits).Scan(&referralID, &rewardedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The unique invitee key won a race with a concurrent claim. Return that
		// referral; this transaction never credited anything.
		existing, found, readErr := existingReferralTx(ctx, tx, in.InviteeUserID)
		if readErr != nil {
			return ClaimReferralResult{}, readErr
		}
		if !found {
			return ClaimReferralResult{}, errors.New("referral: claim conflict without an existing referral")
		}
		return existing, tx.Commit(ctx)
	}
	if err != nil {
		return ClaimReferralResult{}, err
	}

	inviterBalance, err := creditReferralWallet(ctx, tx, ownerOrgID, inviterCredits,
		"referral_inviter", referralID, "referral_inviter:"+referralID)
	if err != nil {
		return ClaimReferralResult{}, err
	}
	inviteeBalance, err := creditReferralWallet(ctx, tx, in.InviteeOrgID, inviteeCredits,
		"referral_invitee", referralID, "referral_invitee:"+referralID)
	if err != nil {
		return ClaimReferralResult{}, err
	}

	if _, err := tx.Exec(ctx, `update billing.referral_codes set uses=uses+1 where code=$1`, code); err != nil {
		return ClaimReferralResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ClaimReferralResult{}, err
	}
	return ClaimReferralResult{
		ReferralID:            referralID,
		Code:                  code,
		Status:                ReferralStatusRewarded,
		AlreadyClaimed:        false,
		InviterCredits:        inviterCredits,
		InviteeCredits:        inviteeCredits,
		BalanceCredits:        inviteeBalance,
		InviterBalanceCredits: inviterBalance,
		RewardedAt:            &rewardedAt,
	}, nil
}

// AdminReferrals returns platform-level referral totals and the newest rows.
func (s *Store) AdminReferrals(ctx context.Context, limit int) (AdminReferrals, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := AdminReferrals{Referrals: make([]AdminReferralRow, 0)}
	if err := s.Pool.QueryRow(ctx, `
		select count(*)::bigint,
		       count(*) filter (where status='rewarded')::bigint,
		       coalesce(sum(inviter_credits),0)::bigint,
		       coalesce(sum(invitee_credits),0)::bigint
		from billing.referrals`).
		Scan(&out.TotalReferrals, &out.RewardedReferrals,
			&out.TotalInviterCredits, &out.TotalInviteeCredits); err != nil {
		return AdminReferrals{}, err
	}
	rows, err := s.Pool.Query(ctx, `
		select r.id::text, r.code,
		       r.inviter_user_id::text, r.inviter_org_id::text, inviter.name,
		       r.invitee_user_id::text, r.invitee_org_id::text, invitee.name,
		       r.status, r.inviter_credits, r.invitee_credits,
		       r.created_at, r.rewarded_at
		from billing.referrals r
		join public.organizations inviter on inviter.id = r.inviter_org_id
		join public.organizations invitee on invitee.id = r.invitee_org_id
		order by r.created_at desc
		limit $1`, limit)
	if err != nil {
		return AdminReferrals{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var row AdminReferralRow
		if err := rows.Scan(
			&row.ID, &row.Code,
			&row.InviterUserID, &row.InviterOrgID, &row.InviterOrgName,
			&row.InviteeUserID, &row.InviteeOrgID, &row.InviteeOrgName,
			&row.Status, &row.InviterCredits, &row.InviteeCredits,
			&row.CreatedAt, &row.RewardedAt,
		); err != nil {
			return AdminReferrals{}, err
		}
		out.Referrals = append(out.Referrals, row)
	}
	return out, rows.Err()
}

// referralClaim is the pure input to validateReferralClaim. Keeping the checks
// separate from SQL makes every rejection path unit-testable.
type referralClaim struct {
	CodeActive       bool
	Uses             int
	MaxUses          int
	InviterUserID    string
	InviterOrgID     string
	InviteeUserID    string
	InviteeOrgID     string
	AccountCreatedAt time.Time
}

func validateReferralClaim(c referralClaim, now time.Time, window time.Duration) error {
	if !c.CodeActive {
		return ErrReferralCodeInactive
	}
	if c.MaxUses > 0 && c.Uses >= c.MaxUses {
		return ErrReferralCodeExhausted
	}
	if c.InviterOrgID == "" || c.InviteeOrgID == "" {
		return ErrReferralNotEligible
	}
	if c.InviterOrgID == c.InviteeOrgID || c.InviterUserID == c.InviteeUserID {
		return ErrReferralSelfClaim
	}
	if c.AccountCreatedAt.IsZero() {
		return ErrReferralInviteeMissing
	}
	if window <= 0 {
		window = ReferralAccountAgeWindow
	}
	if now.Sub(c.AccountCreatedAt) > window {
		return ErrReferralAccountTooOld
	}
	return nil
}

// existingReferralTx returns the invitee's rewarded referral, if any. It is the
// idempotent replay path and the unique-race fallback.
func existingReferralTx(ctx context.Context, tx pgx.Tx, inviteeUserID string) (ClaimReferralResult, bool, error) {
	var (
		out          ClaimReferralResult
		inviteeOrgID string
		rewardedAt   *time.Time
	)
	err := tx.QueryRow(ctx, `
		select id::text, code, invitee_org_id::text, inviter_credits, invitee_credits, rewarded_at
		from billing.referrals
		where invitee_user_id=$1`, inviteeUserID).
		Scan(&out.ReferralID, &out.Code, &inviteeOrgID, &out.InviterCredits, &out.InviteeCredits, &rewardedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimReferralResult{}, false, nil
	}
	if err != nil {
		return ClaimReferralResult{}, false, err
	}
	balance, err := referralWalletBalanceTx(ctx, tx, inviteeOrgID)
	if err != nil {
		return ClaimReferralResult{}, false, err
	}
	out.Status = ReferralStatusRewarded
	out.AlreadyClaimed = true
	out.BalanceCredits = balance
	out.RewardedAt = rewardedAt
	return out, true, nil
}

func referralAccountCreatedAt(ctx context.Context, tx pgx.Tx, userID string) (time.Time, error) {
	var created time.Time
	err := tx.QueryRow(ctx, `select created_at from auth.users where id=$1`, userID).Scan(&created)
	return created, err
}

// lockReferralWallets takes wallet advisory locks in a stable order so two
// claims that touch the same two wallets cannot deadlock.
func lockReferralWallets(ctx context.Context, tx pgx.Tx, orgIDs ...string) error {
	seen := make(map[string]bool, len(orgIDs))
	unique := make([]string, 0, len(orgIDs))
	for _, orgID := range orgIDs {
		if orgID == "" || seen[orgID] {
			continue
		}
		seen[orgID] = true
		unique = append(unique, orgID)
	}
	sort.Strings(unique)
	for _, orgID := range unique {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "wallet:"+orgID); err != nil {
			return err
		}
	}
	return nil
}

// creditReferralWallet books one reward. The ledger row is the audit record and
// the wallet is updated only when that row was actually inserted, so a replay
// cannot double-credit. The idempotency key is derived from the referral id and
// side, not from request input.
func creditReferralWallet(ctx context.Context, tx pgx.Tx, orgID string, credits int64, reason, refID, idempotencyKey string) (int64, error) {
	if credits > 0 {
		tag, err := tx.Exec(ctx, `
			insert into billing.ledger (org_id,delta_credits,reason,ref_id,idempotency_key)
			values ($1,$2,$3,$4,$5)
			on conflict (idempotency_key) do nothing`,
			orgID, credits, reason, refID, idempotencyKey)
		if err != nil {
			return 0, err
		}
		if tag.RowsAffected() == 1 {
			var balance int64
			if err := tx.QueryRow(ctx, `
				insert into billing.wallets as w (org_id,balance_credits)
				values ($1,$2)
				on conflict (org_id) do update
					set balance_credits = w.balance_credits + excluded.balance_credits,
					    version = w.version + 1,
					    updated_at = now()
				returning balance_credits`, orgID, credits).Scan(&balance); err != nil {
				return 0, err
			}
			return balance, nil
		}
	}
	return referralWalletBalanceTx(ctx, tx, orgID)
}

func referralWalletBalanceTx(ctx context.Context, tx pgx.Tx, orgID string) (int64, error) {
	var balance int64
	err := tx.QueryRow(ctx, `
		select coalesce((select balance_credits from billing.wallets where org_id=$1),0)`, orgID).Scan(&balance)
	return balance, err
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
