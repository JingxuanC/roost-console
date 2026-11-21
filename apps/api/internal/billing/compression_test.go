package billing

import (
	"errors"
	"testing"
)

func validCompression() Compression {
	return Compression{
		Applied:               true,
		OriginalInputTokens:   33404,
		CompressedInputTokens: 7378,
		SavedTokens:           26026,
		UserShareTokens:       7807,
		UserShareMinor:        1542,
		SummaryCostMinor:      0,
	}
}

func TestNormalizeCompressionValid(t *testing.T) {
	in := validCompression()
	got, err := NormalizeCompression(&in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || *got != in {
		t.Fatalf("normalized=%+v want=%+v", got, in)
	}
	// The store keeps its own copy; the caller's object is not aliased.
	in.SavedTokens = 1
	if got.SavedTokens != 26026 {
		t.Fatalf("normalized object aliases input: %+v", got)
	}
}

func TestNormalizeCompressionAbsentOrZero(t *testing.T) {
	if got, err := NormalizeCompression(nil); err != nil || got != nil {
		t.Fatalf("absent: got=%v err=%v", got, err)
	}
	for name, in := range map[string]Compression{
		"all zero":           {},
		"applied only":       {Applied: true},
		"applied false zero": {Applied: false},
	} {
		if got, err := NormalizeCompression(&in); err != nil || got != nil {
			t.Fatalf("%s: got=%v err=%v", name, got, err)
		}
	}
}

func TestNormalizeCompressionRejectsNegative(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Compression)
	}{
		{"original", func(c *Compression) { c.OriginalInputTokens = -1 }},
		{"compressed", func(c *Compression) { c.CompressedInputTokens = -1 }},
		{"saved", func(c *Compression) { c.SavedTokens = -1 }},
		{"user share tokens", func(c *Compression) { c.UserShareTokens = -1 }},
		{"user share minor", func(c *Compression) { c.UserShareMinor = -1 }},
		{"summary cost", func(c *Compression) { c.SummaryCostMinor = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCompression()
			tc.mutate(&c)
			if _, err := NormalizeCompression(&c); !errors.Is(err, ErrInvalidCompression) {
				t.Fatalf("err=%v want ErrInvalidCompression", err)
			}
		})
	}
}

func TestCompressionIsZero(t *testing.T) {
	if !(Compression{}).IsZero() {
		t.Fatal("zero value is not zero")
	}
	c := validCompression()
	if c.IsZero() {
		t.Fatal("nonzero compression reported as zero")
	}
	c.SavedTokens = 0
	c.OriginalInputTokens = 0
	c.CompressedInputTokens = 0
	c.UserShareTokens = 0
	c.UserShareMinor = 0
	c.SummaryCostMinor = 0
	if !c.IsZero() {
		t.Fatal("all-zero counters reported as nonzero")
	}
}
