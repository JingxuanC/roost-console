package billing

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validSandboxInput() SandboxUsageInput {
	return SandboxUsageInput{
		OrgID:             uuid.NewString(),
		UserID:            "user-1",
		Product:           "market",
		SandboxID:         "sbx-1",
		Provider:          "daytona",
		ProviderSandboxID: "daytona-1",
		RunID:             "run-1",
		BillableSeconds:   3661,
		CPUSeconds:        3661,
		MemoryMiBSeconds:  3661 * 1024,
		DiskMiBSeconds:    3661 * 256,
		GPUSeconds:        0,
		CPUCredits:        0.5,
		MemoryCredits:     1.25,
		DiskCredits:       0.25,
		Currency:          "cny",
		UnitPrices:        json.RawMessage(`{"cpu_credits_per_second":0.0001}`),
		IdempotencyKey:    "sbx-run-1",
	}
}

func TestNormalizeSandboxUsageDerivesTotalAndMinor(t *testing.T) {
	in := validSandboxInput()
	in.Product = "  Market  "
	in.IdempotencyKey = "  sbx-run-1  "
	started := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	ended := started.Add(time.Hour)
	in.StartedAt, in.EndedAt = &started, &ended

	u, err := NormalizeSandboxUsage(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// 0.5 + 1.25 + 0.25 = 2.0 credits.
	if u.TotalCredits != 2 || u.CreditsMinor != 2*SandboxCreditsMinorPerCredit {
		t.Fatalf("total=%d minor=%d", u.TotalCredits, u.CreditsMinor)
	}
	if u.Product != "market" || u.IdempotencyKey != "sbx-run-1" {
		t.Fatalf("normalized product=%q key=%q", u.Product, u.IdempotencyKey)
	}
	if u.Currency != "CNY" {
		t.Fatalf("currency=%q want CNY", u.Currency)
	}
	if string(u.UnitPrices) != `{"cpu_credits_per_second":0.0001}` {
		t.Fatalf("unit prices=%s", u.UnitPrices)
	}
	if u.BillableSeconds != 3661 || u.CPUSeconds != 3661 {
		t.Fatalf("seconds=%d cpu=%d", u.BillableSeconds, u.CPUSeconds)
	}
}

func TestNormalizeSandboxUsageRoundsComponentTotalUp(t *testing.T) {
	in := validSandboxInput()
	in.CPUCredits, in.MemoryCredits, in.DiskCredits, in.GPUCredits = 0.1, 0.1, 0.1, 0.1
	u, err := NormalizeSandboxUsage(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// 0.4 credits still bills one whole credit rather than zero.
	if u.TotalCredits != 1 || u.CreditsMinor != 1000 {
		t.Fatalf("total=%d minor=%d", u.TotalCredits, u.CreditsMinor)
	}
}

func TestNormalizeSandboxUsageExplicitTotalWins(t *testing.T) {
	in := validSandboxInput()
	five := int64(5)
	in.TotalCredits = &five
	u, err := NormalizeSandboxUsage(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if u.TotalCredits != 5 || u.CreditsMinor != 5000 {
		t.Fatalf("total=%d minor=%d", u.TotalCredits, u.CreditsMinor)
	}
}

func TestNormalizeSandboxUsageRejectsInvalid(t *testing.T) {
	zero := int64(0)
	negativeTotal := int64(-2)
	tooBig := int64(math.MaxInt64/SandboxCreditsMinorPerCredit + 1)
	started := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	before := started.Add(-time.Minute)

	cases := map[string]func(in *SandboxUsageInput){
		"missing org":            func(in *SandboxUsageInput) { in.OrgID = "" },
		"bad org uuid":           func(in *SandboxUsageInput) { in.OrgID = "org-1" },
		"missing product":        func(in *SandboxUsageInput) { in.Product = "  " },
		"missing sandbox":        func(in *SandboxUsageInput) { in.SandboxID = "" },
		"missing key":            func(in *SandboxUsageInput) { in.IdempotencyKey = " " },
		"long key":               func(in *SandboxUsageInput) { in.IdempotencyKey = strings.Repeat("k", sandboxMaxKeyLen+1) },
		"long sandbox":           func(in *SandboxUsageInput) { in.SandboxID = strings.Repeat("s", sandboxMaxIDLen+1) },
		"negative seconds":       func(in *SandboxUsageInput) { in.CPUSeconds = -1 },
		"negative credits":       func(in *SandboxUsageInput) { in.CPUCredits = -0.5 },
		"nan credits":            func(in *SandboxUsageInput) { in.CPUCredits = math.NaN() },
		"infinite credits":       func(in *SandboxUsageInput) { in.CPUCredits = math.Inf(1) },
		"component out of range": func(in *SandboxUsageInput) { in.CPUCredits = sandboxMaxCreditsComponent },
		"zero total":             func(in *SandboxUsageInput) { in.TotalCredits = &zero },
		"negative total":         func(in *SandboxUsageInput) { in.TotalCredits = &negativeTotal },
		"total overflow":         func(in *SandboxUsageInput) { in.TotalCredits = &tooBig },
		"no total or components": func(in *SandboxUsageInput) {
			in.CPUCredits, in.MemoryCredits, in.DiskCredits, in.GPUCredits = 0, 0, 0, 0
		},
		"ended before started": func(in *SandboxUsageInput) { in.StartedAt, in.EndedAt = &started, &before },
		"unit prices array":    func(in *SandboxUsageInput) { in.UnitPrices = json.RawMessage(`[1,2]`) },
		"unit prices invalid":  func(in *SandboxUsageInput) { in.UnitPrices = json.RawMessage(`{`) },
		"long currency":        func(in *SandboxUsageInput) { in.Currency = strings.Repeat("C", sandboxMaxCurrencyLen+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := validSandboxInput()
			mutate(&in)
			if _, err := NormalizeSandboxUsage(in); !errors.Is(err, ErrInvalidSandboxUsage) {
				t.Fatalf("err=%v want ErrInvalidSandboxUsage", err)
			}
		})
	}
}

func TestNormalizeSandboxUsageDefaultsCurrency(t *testing.T) {
	in := validSandboxInput()
	in.Currency = ""
	u, err := NormalizeSandboxUsage(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if u.Currency != "CNY" {
		t.Fatalf("currency=%q", u.Currency)
	}
}
