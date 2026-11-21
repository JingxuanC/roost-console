package billing

import (
	"errors"
	"testing"
)

func TestCustomTopUpQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cents   int64
		wantErr bool
	}{
		{999, true}, {1000, false}, {2000, false}, {500000, false}, {500001, true},
	}
	for _, tc := range cases {
		pack, err := CustomTopUpQuote(tc.cents)
		if tc.wantErr {
			if !errors.Is(err, ErrInvalidTopUpAmount) {
				t.Fatalf("cents=%d err=%v want ErrInvalidTopUpAmount", tc.cents, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("cents=%d err=%v", tc.cents, err)
		}
		if pack.AmountCents != int(tc.cents) || pack.Credits != tc.cents || pack.SKU != CustomTopUpSKU {
			t.Fatalf("cents=%d pack=%+v", tc.cents, pack)
		}
	}
}
