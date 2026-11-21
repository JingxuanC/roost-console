package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

// referralBilling is the slice of the billing store used by the invite-code
// endpoints. It is an interface so auth, owner checks and claim error mapping can
// be tested without Postgres.
type referralBilling interface {
	EnsureOrg(ctx context.Context, userID, email string) (billing.Org, error)
	EnsureOrgByUserID(ctx context.Context, userID string) (billing.Org, error)
	ReferralOverview(ctx context.Context, userID, orgID string, maxUses int) (billing.ReferralOverview, error)
	ClaimReferral(ctx context.Context, in billing.ClaimReferralInput) (billing.ClaimReferralResult, error)
}

func (s *Server) referralService() referralBilling {
	if s.referrals != nil {
		return s.referrals
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

// The link is the public console entry point. It is intentionally independent
// from API host configuration: the referral program shares one website URL.
const referralLinkPrefix = "https://www.roostbots.com/console/?ref="

func referralLink(code string) string {
	return referralLinkPrefix + code
}

// referralOverview returns the caller organization's invite code and reward
// totals. Creating the code is restricted to the organization owner; members can
// still read their own wallet but cannot mint an invite link for someone else's
// organization.
func (s *Server) referralOverview(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	service := s.referralService()
	if service == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "referral store is not configured")
		return
	}
	org, err := service.EnsureOrg(r.Context(), u.ID, u.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	if org.Role != "owner" {
		writeError(w, http.StatusForbidden, "forbidden", "only the organization owner can create a referral link")
		return
	}
	overview, err := service.ReferralOverview(r.Context(), u.ID, org.ID, s.cfg.ReferralMaxUses)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":                  overview.Code,
		"link":                  referralLink(overview.Code),
		"invited_count":         overview.InvitedCount,
		"inviter_credits":       s.cfg.ReferralInviterCredits,
		"invitee_credits":       s.cfg.ReferralInviteeCredits,
		"total_inviter_credits": overview.TotalInviterCredits,
		"total_invitee_credits": overview.TotalInviteeCredits,
	})
}

// claimReferral rewards the current user's organization for a code they were
// invited with. The store owns atomicity and idempotency; this handler only
// validates the shape and maps failures to HTTP.
func (s *Server) claimReferral(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	code := billing.NormalizeReferralCode(req.Code)
	if code == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "code is required")
		return
	}
	if len(code) > 64 {
		writeError(w, http.StatusBadRequest, "bad_request", "code is too long")
		return
	}
	service := s.referralService()
	if service == nil {
		writeError(w, http.StatusServiceUnavailable, "internal_error", "referral store is not configured")
		return
	}
	org, err := service.EnsureOrgByUserID(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	accountCreated, _ := time.Parse(time.RFC3339, u.CreatedAt)
	result, err := service.ClaimReferral(r.Context(), billing.ClaimReferralInput{
		Code:             code,
		InviteeUserID:    u.ID,
		InviteeOrgID:     org.ID,
		InviterCredits:   s.cfg.ReferralInviterCredits,
		InviteeCredits:   s.cfg.ReferralInviteeCredits,
		AccountCreatedAt: accountCreated,
	})
	if err != nil {
		s.failReferralClaim(w, err)
		return
	}
	response := map[string]any{
		"ok":              true,
		"referral_id":     result.ReferralID,
		"code":            result.Code,
		"status":          result.Status,
		"already_claimed": result.AlreadyClaimed,
		"inviter_credits": result.InviterCredits,
		"invitee_credits": result.InviteeCredits,
		"balance_credits": result.BalanceCredits,
	}
	if result.RewardedAt != nil {
		response["rewarded_at"] = result.RewardedAt
	}
	writeJSON(w, http.StatusOK, response)
}

// failReferralClaim keeps invalid-code and anti-abuse refusals as client errors;
// unexpected database errors still go through the generic 500 path.
func (s *Server) failReferralClaim(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrReferralCodeNotFound):
		writeError(w, http.StatusNotFound, "not_found", "referral code not found")
	case errors.Is(err, billing.ErrReferralCodeInactive),
		errors.Is(err, billing.ErrReferralCodeExhausted),
		errors.Is(err, billing.ErrReferralSelfClaim),
		errors.Is(err, billing.ErrReferralAccountTooOld),
		errors.Is(err, billing.ErrReferralNotEligible),
		errors.Is(err, billing.ErrReferralInviteeMissing):
		writeError(w, http.StatusBadRequest, "referral_not_eligible", err.Error())
	default:
		s.log.Error("referral claim failed", "err", err)
		s.fail(w, err)
	}
}
