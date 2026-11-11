package config

import (
	"os"
	"strings"
)

type Config struct {
	Port            string
	DatabaseURL     string
	SupabaseURL     string
	SupabaseAnonKey string
	SupabaseDialIP  string
	PublicBaseURL   string
	ConsoleOrigin   string
	ServiceSecret   string
	XunhuAppID      string
	XunhuAppSecret  string
	XunhuAPIBase    string
	XunhuNotifyURL  string
	XunhuReturnURL  string
	Environment     string
	OIDCIssuer      string
	OIDCClientID    string
	OIDCClientSecret string
	OIDCRedirectURIs string
	OIDCConsentURL  string
}

func Load() Config {
	get := func(k, d string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return d
	}
	return Config{
		Port:            get("PORT", "8080"),
		DatabaseURL:     get("DATABASE_URL", ""),
		SupabaseURL:     strings.TrimRight(get("SUPABASE_URL", ""), "/"),
		SupabaseAnonKey: get("SUPABASE_ANON_KEY", ""),
		SupabaseDialIP:  get("SUPABASE_DIAL_IP", ""),
		PublicBaseURL:   strings.TrimRight(get("PUBLIC_BASE_URL", "http://127.0.0.1:8080"), "/"),
		ConsoleOrigin:   get("CONSOLE_ORIGIN", "http://127.0.0.1:5173"),
		ServiceSecret:   get("SERVICE_SECRET", "change-me"),
		XunhuAppID:      get("XUNHUPAY_APPID", ""),
		XunhuAppSecret:  get("XUNHUPAY_APPSECRET", ""),
		XunhuAPIBase:    get("XUNHUPAY_API_BASE", "https://api.xunhupay.com/payment/do.html"),
		XunhuNotifyURL:  get("XUNHUPAY_NOTIFY_URL", ""),
		XunhuReturnURL:  get("XUNHUPAY_RETURN_URL", ""),
		Environment:     get("APP_ENV", "development"),
		OIDCIssuer:      strings.TrimRight(get("OIDC_ISSUER", "https://www.roostbots.com/console-api"), "/"),
		OIDCClientID:    get("OIDC_CLIENT_ID", "roostbot"),
		OIDCClientSecret: get("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURIs: get("OIDC_REDIRECT_URIS", ""),
		OIDCConsentURL:  get("OIDC_CONSENT_URL", "https://www.roostbots.com/console/oauth/authorize"),
	}
}

func (c Config) PaymentConfigured() bool {
	return c.XunhuAppID != "" && c.XunhuAppSecret != ""
}
