package billing

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func validToolCharge() ToolCharge {
	return ToolCharge{
		Tool:              "search",
		Unit:              "call",
		Quantity:          1,
		UnitPriceMicro:    700000,
		AmountMinor:       7,
		ProviderCostMicro: 750,
		Status:            "succeeded",
	}
}

func TestNormalizeToolCharges(t *testing.T) {
	got, err := NormalizeToolCharges([]ToolCharge{{
		Tool:              "  search ",
		Unit:              " CALL ",
		Quantity:          1,
		UnitPriceMicro:    700000,
		AmountMinor:       7,
		ProviderCostMicro: 750,
		Status:            " SUCCEEDED ",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := validToolCharge()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("normalized=%+v want=%+v", got, want)
	}
}

func TestNormalizeToolChargesEmpty(t *testing.T) {
	for _, in := range [][]ToolCharge{nil, {}} {
		got, err := NormalizeToolCharges(in)
		if err != nil || got != nil {
			t.Fatalf("empty input: got=%v err=%v", got, err)
		}
	}
}

func TestNormalizeToolChargesRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ToolCharge)
	}{
		{"empty tool", func(c *ToolCharge) { c.Tool = "  " }},
		{"long tool", func(c *ToolCharge) { c.Tool = strings.Repeat("x", maxToolChargeToolLen+1) }},
		{"empty unit", func(c *ToolCharge) { c.Unit = "" }},
		{"unknown unit", func(c *ToolCharge) { c.Unit = "token" }},
		{"long unit", func(c *ToolCharge) { c.Unit = strings.Repeat("x", maxToolChargeUnitLen+1) }},
		{"zero quantity", func(c *ToolCharge) { c.Quantity = 0 }},
		{"negative quantity", func(c *ToolCharge) { c.Quantity = -1 }},
		{"NaN quantity", func(c *ToolCharge) { c.Quantity = math.NaN() }},
		{"infinite quantity", func(c *ToolCharge) { c.Quantity = math.Inf(1) }},
		{"negative unit price", func(c *ToolCharge) { c.UnitPriceMicro = -1 }},
		{"negative amount", func(c *ToolCharge) { c.AmountMinor = -1 }},
		{"negative provider cost", func(c *ToolCharge) { c.ProviderCostMicro = -1 }},
		{"empty status", func(c *ToolCharge) { c.Status = "" }},
		{"unknown status", func(c *ToolCharge) { c.Status = "failed" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validToolCharge()
			tc.mutate(&c)
			if _, err := NormalizeToolCharges([]ToolCharge{c}); !errors.Is(err, ErrInvalidToolCharges) {
				t.Fatalf("err=%v want ErrInvalidToolCharges", err)
			}
		})
	}
}

func TestNormalizeToolChargesCapsLength(t *testing.T) {
	charges := make([]ToolCharge, MaxToolCharges+1)
	for i := range charges {
		charges[i] = validToolCharge()
	}
	if _, err := NormalizeToolCharges(charges); !errors.Is(err, ErrInvalidToolCharges) {
		t.Fatalf("err=%v want ErrInvalidToolCharges", err)
	}
	if _, err := NormalizeToolCharges(charges[:MaxToolCharges]); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
}

func TestSumToolCharges(t *testing.T) {
	totals := SumToolCharges([]ToolCharge{
		{AmountMinor: 7, ProviderCostMicro: 750},
		{AmountMinor: 100, ProviderCostMicro: 120},
	})
	if totals.AmountMinor != 107 || totals.ProviderCostMicro != 870 {
		t.Fatalf("totals=%+v", totals)
	}
	if got := SumToolCharges(nil); got != (ToolChargeTotals{}) {
		t.Fatalf("nil totals=%+v", got)
	}
}
