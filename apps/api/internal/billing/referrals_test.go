package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAdminGiftCreditsValidationWithoutDB(t *testing.T) {
	store := &Store{}
	ctx := context.Background()
	if _, err := store.AdminGiftCredits(ctx, "org-1", 0, "zero", "key-1", "ops"); !errors.Is(err, ErrInvalidGiftCredits) {
		t.Fatalf("zero err=%v", err)
	}
	if _, err := store.AdminGiftCredits(ctx, "org-1", -1, "negative", "key-1", "ops"); !errors.Is(err, ErrInvalidGiftCredits) {
		t.Fatalf("negative err=%v", err)
	}
	if _, err := store.AdminGiftCredits(ctx, "org-1", 10, "no key", "  ", "ops"); !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("missing key err=%v", err)
	}
}

func TestNewReferralCodeFormat(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		code, err := newReferralCode()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(code) != referralCodeLength {
			t.Fatalf("code %q length=%d", code, len(code))
		}
		if code != strings.ToUpper(code) {
			t.Fatalf("code %q is not uppercase", code)
		}
		for _, r := range code {
			if !strings.ContainsRune(referralCodeAlphabet, r) {
				t.Fatalf("code %q contains ambiguous/unsafe rune %q", code, r)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = true
	}
}

func TestNormalizeReferralCode(t *testing.T) {
	for raw, want := range map[string]string{
		"  abcd1234 ": "ABCD1234",
		"ABCD1234":    "ABCD1234",
		"":            "",
		"   ":         "",
	} {
		if got := NormalizeReferralCode(raw); got != want {
			t.Fatalf("NormalizeReferralCode(%q)=%q want %q", raw, got, want)
		}
	}
}

func TestValidateReferralClaim(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	valid := referralClaim{
		CodeActive:       true,
		Uses:             0,
		MaxUses:          0,
		InviterUserID:    "inviter-user",
		InviterOrgID:     "inviter-org",
		InviteeUserID:    "invitee-user",
		InviteeOrgID:     "invitee-org",
		AccountCreatedAt: now.Add(-24 * time.Hour),
	}
	if err := validateReferralClaim(valid, now, ReferralAccountAgeWindow); err != nil {
		t.Fatalf("valid claim rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*referralClaim)
		now    time.Time
		want   error
	}{
		{"inactive", func(c *referralClaim) { c.CodeActive = false }, now, ErrReferralCodeInactive},
		{"exhausted", func(c *referralClaim) { c.Uses = 5; c.MaxUses = 5 }, now, ErrReferralCodeExhausted},
		{"self-org", func(c *referralClaim) { c.InviteeOrgID = c.InviterOrgID }, now, ErrReferralSelfClaim},
		{"self-user", func(c *referralClaim) { c.InviteeUserID = c.InviterUserID }, now, ErrReferralSelfClaim},
		{"missing-invitee-org", func(c *referralClaim) { c.InviteeOrgID = "" }, now, ErrReferralNotEligible},
		{"zero-account-age", func(c *referralClaim) { c.AccountCreatedAt = time.Time{} }, now, ErrReferralInviteeMissing},
		{"too-old", func(c *referralClaim) {
			c.AccountCreatedAt = now.Add(-ReferralAccountAgeWindow - time.Second)
		}, now, ErrReferralAccountTooOld},
		{"exactly-window", func(c *referralClaim) {
			c.AccountCreatedAt = now.Add(-ReferralAccountAgeWindow)
		}, now, nil},
		{"future-account-clock-skew", func(c *referralClaim) {
			c.AccountCreatedAt = now.Add(time.Hour)
		}, now, nil},
		{"limited-but-room", func(c *referralClaim) { c.Uses = 4; c.MaxUses = 5 }, now, nil},
	}
	for _, tc := range cases {
		claim := valid
		tc.mutate(&claim)
		err := validateReferralClaim(claim, tc.now, ReferralAccountAgeWindow)
		if tc.want == nil {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err=%v want %v", tc.name, err, tc.want)
		}
	}
}

func TestValidateReferralClaimUsesDefaultWindow(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	claim := referralClaim{
		CodeActive:       true,
		InviterUserID:    "inviter-user",
		InviterOrgID:     "inviter-org",
		InviteeUserID:    "invitee-user",
		InviteeOrgID:     "invitee-org",
		AccountCreatedAt: now.Add(-8 * 24 * time.Hour),
	}
	if err := validateReferralClaim(claim, now, 0); !errors.Is(err, ErrReferralAccountTooOld) {
		t.Fatalf("err=%v want ErrReferralAccountTooOld", err)
	}
}
