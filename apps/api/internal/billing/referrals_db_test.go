package billing

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newReferralTestUserOrg creates an auth user, an organization and a funded
// wallet. Cleanup removes the organization (cascading wallets, ledger,
// referral_codes and referrals) and then the auth user.
func newReferralTestUserOrg(t *testing.T, pool *pgxpool.Pool, balance int64, createdAt *time.Time) (string, string) {
	t.Helper()
	ctx := context.Background()
	userID := uuid.NewString()
	orgID := uuid.NewString()
	var err error
	if createdAt == nil {
		_, err = pool.Exec(ctx, `insert into auth.users (id,email) values ($1,$2)`, userID, userID+"@test.local")
	} else {
		_, err = pool.Exec(ctx, `insert into auth.users (id,email,created_at) values ($1,$2,$3)`,
			userID, userID+"@test.local", *createdAt)
	}
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from public.organizations where id=$1`, orgID)
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id=$1`, userID)
	})
	if _, err := pool.Exec(ctx, `
		insert into public.organizations (id,name,slug,owner_user_id,plan_code)
		values ($1,'Referral Test',$2,$3,'free')`, orgID, "referral-"+userID[:8], userID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into billing.wallets (org_id,balance_credits) values ($1,$2)`, orgID, balance); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	return userID, orgID
}

func walletBalanceDB(t *testing.T, pool *pgxpool.Pool, orgID string) int64 {
	t.Helper()
	var balance int64
	if err := pool.QueryRow(context.Background(),
		`select balance_credits from billing.wallets where org_id=$1`, orgID).Scan(&balance); err != nil {
		t.Fatalf("read wallet: %v", err)
	}
	return balance
}

// TestReferralClaimDB exercises the complete atomic claim path against Postgres:
// code creation, first reward, replay idempotency, ledger/wallet consistency,
// the use counter, self-claim refusal and the seven-day account-age gate. It is
// skipped unless ROOST_TEST_DATABASE_URL points at a database with migrations
// 0001..0011 applied.
func TestReferralClaimDB(t *testing.T) {
	databaseURL := os.Getenv("ROOST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ROOST_TEST_DATABASE_URL to run the referral database test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	store := New(pool)

	inviterUser, inviterOrg := newReferralTestUserOrg(t, pool, 500, nil)
	inviteeUser, inviteeOrg := newReferralTestUserOrg(t, pool, 1000, nil)

	overview, err := store.ReferralOverview(ctx, inviterUser, inviterOrg, 0)
	if err != nil {
		t.Fatalf("referral overview: %v", err)
	}
	if len(overview.Code) != referralCodeLength {
		t.Fatalf("code=%q", overview.Code)
	}
	// The ensure path is idempotent: a second read returns the same code.
	again, err := store.ReferralOverview(ctx, inviterUser, inviterOrg, 0)
	if err != nil {
		t.Fatalf("referral overview again: %v", err)
	}
	if again.Code != overview.Code {
		t.Fatalf("code changed: %q -> %q", overview.Code, again.Code)
	}

	result, err := store.ClaimReferral(ctx, ClaimReferralInput{
		Code:           overview.Code,
		InviteeUserID:  inviteeUser,
		InviteeOrgID:   inviteeOrg,
		InviterCredits: 1000,
		InviteeCredits: 1000,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if result.AlreadyClaimed || result.Status != ReferralStatusRewarded ||
		result.InviterCredits != 1000 || result.InviteeCredits != 1000 ||
		result.BalanceCredits != 2000 {
		t.Fatalf("claim result=%+v", result)
	}
	if got := walletBalanceDB(t, pool, inviterOrg); got != 1500 {
		t.Fatalf("inviter balance=%d want 1500", got)
	}
	if got := walletBalanceDB(t, pool, inviteeOrg); got != 2000 {
		t.Fatalf("invitee balance=%d want 2000", got)
	}

	for _, want := range []struct {
		reason string
		delta  int64
	}{
		{"referral_inviter", 1000},
		{"referral_invitee", 1000},
	} {
		var delta int64
		var refID string
		if err := pool.QueryRow(ctx, `
			select delta_credits, coalesce(ref_id,'')
			from billing.ledger where org_id=$1 and reason=$2`,
			map[string]string{"referral_inviter": inviterOrg, "referral_invitee": inviteeOrg}[want.reason],
			want.reason).Scan(&delta, &refID); err != nil {
			t.Fatalf("read ledger %s: %v", want.reason, err)
		}
		if delta != want.delta || refID != result.ReferralID {
			t.Fatalf("ledger %s delta=%d ref=%q", want.reason, delta, refID)
		}
	}
	var (
		status         string
		storedInviter  int64
		storedInvitee  int64
		uses           int
		rewardedAtNull bool
	)
	if err := pool.QueryRow(ctx, `
		select status, inviter_credits, invitee_credits, rewarded_at is null
		from billing.referrals where invitee_user_id=$1`, inviteeUser).
		Scan(&status, &storedInviter, &storedInvitee, &rewardedAtNull); err != nil {
		t.Fatalf("read referral: %v", err)
	}
	if status != "rewarded" || storedInviter != 1000 || storedInvitee != 1000 || rewardedAtNull {
		t.Fatalf("referral status=%q inviter=%d invitee=%d rewarded_at_null=%v",
			status, storedInviter, storedInvitee, rewardedAtNull)
	}
	if err := pool.QueryRow(ctx, `select uses from billing.referral_codes where code=$1`, overview.Code).Scan(&uses); err != nil {
		t.Fatalf("read uses: %v", err)
	}
	if uses != 1 {
		t.Fatalf("uses=%d want 1", uses)
	}

	// Replay: the same invitee with the same (or another) code must return the
	// existing referral without a second ledger row or credit.
	replay, err := store.ClaimReferral(ctx, ClaimReferralInput{
		Code:           overview.Code,
		InviteeUserID:  inviteeUser,
		InviteeOrgID:   inviteeOrg,
		InviterCredits: 9999,
		InviteeCredits: 9999,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.AlreadyClaimed || replay.ReferralID != result.ReferralID ||
		replay.InviterCredits != 1000 || replay.InviteeCredits != 1000 {
		t.Fatalf("replay=%+v", replay)
	}
	if got := walletBalanceDB(t, pool, inviterOrg); got != 1500 {
		t.Fatalf("inviter balance after replay=%d", got)
	}
	if got := walletBalanceDB(t, pool, inviteeOrg); got != 2000 {
		t.Fatalf("invitee balance after replay=%d", got)
	}
	var ledgerRows, referralRows int64
	if err := pool.QueryRow(ctx, `select count(*) from billing.ledger where reason in ('referral_inviter','referral_invitee') and org_id = any($1)`,
		[]string{inviterOrg, inviteeOrg}).Scan(&ledgerRows); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from billing.referrals where invitee_user_id=$1`, inviteeUser).Scan(&referralRows); err != nil {
		t.Fatalf("count referral: %v", err)
	}
	if ledgerRows != 2 || referralRows != 1 {
		t.Fatalf("after replay ledger=%d referrals=%d", ledgerRows, referralRows)
	}

	// The inviter cannot claim their own code.
	if _, err := store.ClaimReferral(ctx, ClaimReferralInput{
		Code:           overview.Code,
		InviteeUserID:  inviterUser,
		InviteeOrgID:   inviterOrg,
		InviterCredits: 1000,
		InviteeCredits: 1000,
	}); !errors.Is(err, ErrReferralSelfClaim) {
		t.Fatalf("self claim err=%v", err)
	}

	// Accounts older than seven days are refused.
	old := time.Now().Add(-8 * 24 * time.Hour)
	oldUser, oldOrg := newReferralTestUserOrg(t, pool, 0, &old)
	if _, err := store.ClaimReferral(ctx, ClaimReferralInput{
		Code:           overview.Code,
		InviteeUserID:  oldUser,
		InviteeOrgID:   oldOrg,
		InviterCredits: 1000,
		InviteeCredits: 1000,
	}); !errors.Is(err, ErrReferralAccountTooOld) {
		t.Fatalf("old account err=%v", err)
	}
	if got := walletBalanceDB(t, pool, oldOrg); got != 0 {
		t.Fatalf("old account was credited: %d", got)
	}

	// max_uses is enforced atomically: the second claim on a one-use code is
	// refused and credits nobody. Deactivating the code then refuses claims too.
	limitedInviter, limitedInviterOrg := newReferralTestUserOrg(t, pool, 0, nil)
	limited, err := store.ReferralOverview(ctx, limitedInviter, limitedInviterOrg, 1)
	if err != nil {
		t.Fatalf("limited code: %v", err)
	}
	firstUser, firstOrg := newReferralTestUserOrg(t, pool, 0, nil)
	if _, err := store.ClaimReferral(ctx, ClaimReferralInput{
		Code:           limited.Code,
		InviteeUserID:  firstUser,
		InviteeOrgID:   firstOrg,
		InviterCredits: 1000,
		InviteeCredits: 1000,
	}); err != nil {
		t.Fatalf("first limited claim: %v", err)
	}
	secondUser, secondOrg := newReferralTestUserOrg(t, pool, 0, nil)
	if _, err := store.ClaimReferral(ctx, ClaimReferralInput{
		Code:           limited.Code,
		InviteeUserID:  secondUser,
		InviteeOrgID:   secondOrg,
		InviterCredits: 1000,
		InviteeCredits: 1000,
	}); !errors.Is(err, ErrReferralCodeExhausted) {
		t.Fatalf("exhausted claim err=%v", err)
	}
	if got := walletBalanceDB(t, pool, secondOrg); got != 0 {
		t.Fatalf("exhausted claim credited: %d", got)
	}
	if _, err := pool.Exec(ctx, `update billing.referral_codes set active=false where code=$1`, limited.Code); err != nil {
		t.Fatalf("deactivate code: %v", err)
	}
	thirdUser, thirdOrg := newReferralTestUserOrg(t, pool, 0, nil)
	if _, err := store.ClaimReferral(ctx, ClaimReferralInput{
		Code:           limited.Code,
		InviteeUserID:  thirdUser,
		InviteeOrgID:   thirdOrg,
		InviterCredits: 1000,
		InviteeCredits: 1000,
	}); !errors.Is(err, ErrReferralCodeInactive) {
		t.Fatalf("inactive claim err=%v", err)
	}
}

// TestAdminGiftCreditsDB proves the explicit gift is positive-only, writes its
// own ledger reason, is idempotent on the client key, and records the operator
// in billing.admin_audit.
func TestAdminGiftCreditsDB(t *testing.T) {
	databaseURL := os.Getenv("ROOST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ROOST_TEST_DATABASE_URL to run the admin gift database test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	store := New(pool)

	_, orgID := newReferralTestUserOrg(t, pool, 100, nil)
	key := "gift-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from billing.admin_audit where target=$1`, orgID)
	})

	balance, err := store.AdminGiftCredits(ctx, orgID, 250, "QA gift", key, "ops@example.com")
	if err != nil {
		t.Fatalf("gift: %v", err)
	}
	if balance != 350 {
		t.Fatalf("balance=%d want 350", balance)
	}
	// Replay with the same key must not add credits.
	replay, err := store.AdminGiftCredits(ctx, orgID, 250, "QA gift", key, "ops@example.com")
	if err != nil {
		t.Fatalf("gift replay: %v", err)
	}
	if replay != 350 {
		t.Fatalf("replay balance=%d", replay)
	}
	if got := walletBalanceDB(t, pool, orgID); got != 350 {
		t.Fatalf("wallet=%d", got)
	}
	var (
		ledgerRows int64
		delta      int64
		note       string
	)
	if err := pool.QueryRow(ctx, `
		select count(*), coalesce(max(delta_credits),0), coalesce(max(ref_id),'')
		from billing.ledger where org_id=$1 and reason='admin_gift'`, orgID).
		Scan(&ledgerRows, &delta, &note); err != nil {
		t.Fatalf("read gift ledger: %v", err)
	}
	if ledgerRows != 1 || delta != 250 || note != "QA gift" {
		t.Fatalf("ledger rows=%d delta=%d note=%q", ledgerRows, delta, note)
	}
	var auditRows int64
	var actor, action string
	if err := pool.QueryRow(ctx, `
		select count(*), coalesce(max(actor),''), coalesce(max(action),'')
		from billing.admin_audit where target=$1`, orgID).
		Scan(&auditRows, &actor, &action); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if auditRows != 1 || actor != "ops@example.com" || action != "admin_gift" {
		t.Fatalf("audit rows=%d actor=%q action=%q", auditRows, actor, action)
	}

	// Validation happens before touching the database.
	if _, err := store.AdminGiftCredits(ctx, orgID, 0, "zero", "gift-zero-"+uuid.NewString(), "ops"); !errors.Is(err, ErrInvalidGiftCredits) {
		t.Fatalf("zero gift err=%v", err)
	}
	if _, err := store.AdminGiftCredits(ctx, orgID, 10, "no key", "", "ops"); !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("missing key err=%v", err)
	}
	if _, err := store.AdminGiftCredits(ctx, uuid.NewString(), 10, "missing org", "gift-missing-"+uuid.NewString(), "ops"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing org err=%v", err)
	}
	// A key already used by another organization (or by the adjustment path)
	// cannot silently skip a real gift.
	_, otherOrgID := newReferralTestUserOrg(t, pool, 0, nil)
	if _, err := store.AdminGiftCredits(ctx, otherOrgID, 250, "QA gift", key, "ops@example.com"); !errors.Is(err, ErrGiftIdempotencyConflict) {
		t.Fatalf("cross-org gift err=%v", err)
	}
	if got := walletBalanceDB(t, pool, otherOrgID); got != 0 {
		t.Fatalf("cross-org gift credited: %d", got)
	}
}
