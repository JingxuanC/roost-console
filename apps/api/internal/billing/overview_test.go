package billing

import "testing"

func TestClampAdminDays(t *testing.T) {
	for _, tc := range []struct {
		in, want int
	}{
		{-5, 1},
		{0, 1},
		{1, 1},
		{30, 30},
		{365, 365},
		{366, AdminOverviewDaysMax},
	} {
		if got := clampAdminDays(tc.in); got != tc.want {
			t.Fatalf("clampAdminDays(%d)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestAdminEmptyProductSpend(t *testing.T) {
	spend := adminEmptyProductSpend()
	for _, product := range []string{
		AdminProductRelay, AdminProductMarket, AdminProductCausalMemory, AdminProductDslpha,
	} {
		if value, ok := spend[product]; !ok || value != 0 {
			t.Fatalf("product %q=%d present=%v want 0", product, value, ok)
		}
	}
}

// Cache hit rate is cache_read / (input + cache_read). A zero cache_read with
// tracked input requests is genuinely 0%, while no data at all is also 0; the
// client uses tracked_requests to distinguish the two states.
func TestAdminCacheHitRate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		read  int64
		input int64
		want  float64
	}{
		{"no data", 0, 0, 0},
		{"cache read only", 100, 0, 1},
		{"tracked but no cache", 0, 1500, 0},
		{"half", 500, 500, 0.5},
		{"partial", 200, 1500, 200.0 / 1700.0},
	} {
		if got := cacheHitRate(tc.read, tc.input); got != tc.want {
			t.Fatalf("%s: cacheHitRate(%d,%d)=%v want %v", tc.name, tc.read, tc.input, got, tc.want)
		}
	}
}
