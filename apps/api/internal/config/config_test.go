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
