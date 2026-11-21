package config

import "testing"

func TestLoadCLIPublicURLDefaults(t *testing.T) {
	t.Setenv("MARKET_API_URL", "")
	t.Setenv("CONSOLE_API_URL", "")
	t.Setenv("CAUSAL_MEMORY_MCP_URL", "")
	t.Setenv("MEMORY_MCP_URL", "")
	cfg := Load()
	if cfg.MarketAPIURL != "https://www.roostbots.com/api" {
		t.Fatalf("market_api_url=%q", cfg.MarketAPIURL)
	}
	if cfg.ConsoleAPIURL != "https://www.roostbots.com/console-api" {
		t.Fatalf("console_api_url=%q", cfg.ConsoleAPIURL)
	}
	if cfg.MemoryMCPURL != "https://causal-memory.com/memory/mcp" {
		t.Fatalf("memory_mcp_url=%q", cfg.MemoryMCPURL)
	}
}

func TestLoadCLIPublicURLOverrides(t *testing.T) {
	t.Setenv("MARKET_API_URL", "https://staging.example.com/api/")
	t.Setenv("CONSOLE_API_URL", "https://staging.example.com/console-api/")
	t.Setenv("CAUSAL_MEMORY_MCP_URL", "https://memory.example.com/memory/mcp/")
	cfg := Load()
	if cfg.MarketAPIURL != "https://staging.example.com/api" {
		t.Fatalf("market_api_url=%q", cfg.MarketAPIURL)
	}
	if cfg.ConsoleAPIURL != "https://staging.example.com/console-api" {
		t.Fatalf("console_api_url=%q", cfg.ConsoleAPIURL)
	}
	if cfg.MemoryMCPURL != "https://memory.example.com/memory/mcp" {
		t.Fatalf("memory_mcp_url=%q", cfg.MemoryMCPURL)
	}
}

func TestLoadReferralDefaults(t *testing.T) {
	t.Setenv("REFERRAL_INVITER_CREDITS", "")
	t.Setenv("REFERRAL_INVITEE_CREDITS", "")
	t.Setenv("REFERRAL_MAX_USES_PER_CODE", "")
	t.Setenv("RATE_REFERRAL_CLAIM_PER_MIN", "")
	cfg := Load()
	if cfg.ReferralInviterCredits != DefaultReferralInviterCredits {
		t.Fatalf("inviter credits=%d", cfg.ReferralInviterCredits)
	}
	if cfg.ReferralInviteeCredits != DefaultReferralInviteeCredits {
		t.Fatalf("invitee credits=%d", cfg.ReferralInviteeCredits)
	}
	if cfg.ReferralMaxUses != 0 {
		t.Fatalf("max uses=%d", cfg.ReferralMaxUses)
	}
	if cfg.RateReferralClaim != 10 {
		t.Fatalf("rate=%d", cfg.RateReferralClaim)
	}
}

func TestLoadReferralOverrides(t *testing.T) {
	t.Setenv("REFERRAL_INVITER_CREDITS", "2500")
	t.Setenv("REFERRAL_INVITEE_CREDITS", "500")
	t.Setenv("REFERRAL_MAX_USES_PER_CODE", "25")
	t.Setenv("RATE_REFERRAL_CLAIM_PER_MIN", "7")
	cfg := Load()
	if cfg.ReferralInviterCredits != 2500 || cfg.ReferralInviteeCredits != 500 {
		t.Fatalf("credits=%d/%d", cfg.ReferralInviterCredits, cfg.ReferralInviteeCredits)
	}
	if cfg.ReferralMaxUses != 25 || cfg.RateReferralClaim != 7 {
		t.Fatalf("max/rate=%d/%d", cfg.ReferralMaxUses, cfg.RateReferralClaim)
	}
}

func TestLoadReferralInvalidValuesFallBack(t *testing.T) {
	t.Setenv("REFERRAL_INVITER_CREDITS", "-10")
	t.Setenv("REFERRAL_INVITEE_CREDITS", "not-a-number")
	t.Setenv("REFERRAL_MAX_USES_PER_CODE", "many")
	t.Setenv("RATE_REFERRAL_CLAIM_PER_MIN", "often")
	cfg := Load()
	if cfg.ReferralInviterCredits != DefaultReferralInviterCredits ||
		cfg.ReferralInviteeCredits != DefaultReferralInviteeCredits {
		t.Fatalf("credits=%d/%d", cfg.ReferralInviterCredits, cfg.ReferralInviteeCredits)
	}
	if cfg.ReferralMaxUses != 0 || cfg.RateReferralClaim != 10 {
		t.Fatalf("max/rate=%d/%d", cfg.ReferralMaxUses, cfg.RateReferralClaim)
	}
}
