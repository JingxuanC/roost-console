package httpapi

import "testing"

func TestServiceScopes(t *testing.T) {
	cases := []struct {
		service, method, path string
		want                  bool
	}{
		{"market", "POST", "/internal/v1/orgs/resolve-user", true},
		{"market", "POST", "/internal/v1/entitlements/resolve", true},
		{"market", "POST", "/internal/v1/usage/leases", true},
		{"market", "POST", "/internal/v1/usage/leases/by-run/draw", true},
		{"market", "POST", "/internal/v1/usage/charge", false},
		{"market", "POST", "/internal/v1/products/causal-memory/tokens", false},
		{"dslpha", "POST", "/api/auth/validate", true},
		{"dslpha", "POST", "/api/auth/billing/leases/acquire", true},
		{"dslpha", "POST", "/internal/v1/usage/charge", false},
		{"causal", "POST", "/internal/v1/usage/charge", true},
		{"causal", "POST", "/internal/v1/usage/leases", false},
		{"causal-sync", "POST", "/internal/v1/products/causal-memory/tokens", true},
		{"causal-sync", "POST", "/internal/v1/usage/charge", false},
		{"unknown", "POST", "/internal/v1/usage/charge", false},
	}
	for _, c := range cases {
		if got := serviceAllows(c.service, c.method, c.path); got != c.want {
			t.Errorf("serviceAllows(%q,%q)=%v want %v", c.service, c.path, got, c.want)
		}
	}
}
