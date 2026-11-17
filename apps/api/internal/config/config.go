package config

import (
	"encoding/json"
	"os"
	"strings"
)

type Config struct {
	Port              string
	DatabaseURL       string
	SupabaseURL       string
	SupabaseAnonKey   string
	SupabaseDialIP    string
	PublicBaseURL     string
	ConsoleOrigin     string
	ServiceSecret     string
	ServiceSecrets    map[string][]string
	XunhuAppID        string
	XunhuAppSecret    string
	XunhuAPIBase      string
	XunhuNotifyURL    string
	XunhuReturnURL    string
	Environment       string
	AdminEmails       []string
	RatePaymentNotify int
	RateOIDCToken     int
	RateProductToken  int
	OIDCIssuer        string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCRedirectURIs  string
	OIDCConsentURL    string
	// Public URLs handed to the CLI bootstrap endpoint. They are display/config
	// values, never secrets.
	MarketAPIURL  string
	ConsoleAPIURL string
	MemoryMCPURL  string
}

func Load() Config {
	get := func(k, d string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return d
	}
	secrets := map[string][]string{}
	if raw := get("SERVICE_SECRETS", ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &secrets)
	}
	if len(secrets) == 0 {
		legacy := get("SERVICE_SECRET", "")
		for _, name := range []string{"market", "dslpha", "causal", "causal-sync", "root"} {
			secrets[name] = []string{legacy}
		}
	}
	return Config{
		Port:              get("PORT", "8080"),
		DatabaseURL:       get("DATABASE_URL", ""),
		SupabaseURL:       strings.TrimRight(get("SUPABASE_URL", ""), "/"),
		SupabaseAnonKey:   get("SUPABASE_ANON_KEY", ""),
		SupabaseDialIP:    get("SUPABASE_DIAL_IP", ""),
		PublicBaseURL:     strings.TrimRight(get("PUBLIC_BASE_URL", "http://127.0.0.1:8080"), "/"),
		ConsoleOrigin:     get("CONSOLE_ORIGIN", "http://127.0.0.1:5173"),
		ServiceSecret:     get("SERVICE_SECRET", "change-me"),
		ServiceSecrets:    secrets,
		XunhuAppID:        get("XUNHUPAY_APPID", ""),
		XunhuAppSecret:    get("XUNHUPAY_APPSECRET", ""),
		XunhuAPIBase:      get("XUNHUPAY_API_BASE", "https://api.xunhupay.com/payment/do.html"),
		XunhuNotifyURL:    get("XUNHUPAY_NOTIFY_URL", ""),
		XunhuReturnURL:    get("XUNHUPAY_RETURN_URL", ""),
		Environment:       get("APP_ENV", "development"),
		RatePaymentNotify: getInt("RATE_PAYMENT_NOTIFY_PER_MIN", 120),
		RateOIDCToken:     getInt("RATE_OIDC_TOKEN_PER_MIN", 60),
		RateProductToken:  getInt("RATE_PRODUCT_TOKEN_PER_MIN", 5),
		AdminEmails:       splitCSV(get("ADMIN_EMAILS", "")),
		OIDCIssuer:        strings.TrimRight(get("OIDC_ISSUER", "https://www.roostbots.com/console-api"), "/"),
		OIDCClientID:      get("OIDC_CLIENT_ID", "roostbot"),
		OIDCClientSecret:  get("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURIs:  get("OIDC_REDIRECT_URIS", ""),
		OIDCConsentURL:    get("OIDC_CONSENT_URL", "https://www.roostbots.com/console/oauth/authorize"),
		MarketAPIURL:      strings.TrimRight(get("MARKET_API_URL", "https://www.roostbots.com/api"), "/"),
		ConsoleAPIURL:     strings.TrimRight(get("CONSOLE_API_URL", "https://www.roostbots.com/console-api"), "/"),
		MemoryMCPURL:      strings.TrimRight(get("CAUSAL_MEMORY_MCP_URL", get("MEMORY_MCP_URL", "https://causal-memory.com/memory/mcp")), "/"),
	}
}

func getInt(k string, d int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return d
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func (c Config) PaymentConfigured() bool {
	return c.XunhuAppID != "" && c.XunhuAppSecret != ""
}

// splitCSV parses a comma separated environment value, trimming blanks.
func splitCSV(raw string) []string {
	out := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, strings.ToLower(part))
		}
	}
	return out
}
