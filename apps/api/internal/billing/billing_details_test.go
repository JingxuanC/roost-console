package billing

import (
	"testing"
	"time"
)

func TestBillingDetailsRange(t *testing.T) {
	now := time.Date(2026, 11, 20, 15, 42, 9, 0, time.UTC)

	start, end := billingDetailsRange(1, now)
	if !start.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("days=1 start=%s", start)
	}
	if !end.Equal(now) {
		t.Fatalf("days=1 end=%s", end)
	}

	start, end = billingDetailsRange(30, now)
	if !start.Equal(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("days=30 start=%s", start)
	}
	if !end.Equal(now) {
		t.Fatalf("days=30 end=%s", end)
	}
}

func TestBillingDetailsYuanConvention(t *testing.T) {
	cases := map[int64]float64{
		0:      0,
		1000:   0.01, // 1 credit = ¥0.01
		100000: 1,    // 100 credits = ¥1
		2500:   0.025,
	}
	for minor, want := range cases {
		if got := billingDetailsYuan(minor); got != want {
			t.Fatalf("billingDetailsYuan(%d)=%v want %v", minor, got, want)
		}
	}
	if SandboxCreditsMinorPerCredit != 1000 {
		t.Fatalf("console convention changed: minor per credit=%d", SandboxCreditsMinorPerCredit)
	}
}
