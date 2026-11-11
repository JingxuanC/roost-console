package payment

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"testing"
)

func TestSignSortedNonEmpty(t *testing.T) {
	v := url.Values{}
	v.Set("b", "2")
	v.Set("a", "1")
	v.Set("empty", "")
	v.Set("hash", "ignored")
	got := Sign(v, "secret")
	want := md5.Sum([]byte("a=1&b=2secret"))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("sign = %s, want %s", got, hex.EncodeToString(want[:]))
	}
}

func TestVerifyRejectsTamperedAmount(t *testing.T) {
	v := url.Values{}
	v.Set("trade_order_id", "ORDER1")
	v.Set("total_fee", "1.00")
	v.Set("status", "OD")
	v.Set("hash", Sign(v, "secret"))
	if !Verify(v, "secret") {
		t.Fatal("valid signature rejected")
	}
	v.Set("total_fee", "0.01")
	if Verify(v, "secret") {
		t.Fatal("tampered amount accepted")
	}
}

func TestAmountCentsFromYuan(t *testing.T) {
	cases := map[string]int{"1": 100, "1.00": 100, "0.01": 1, "49.00": 4900}
	for in, want := range cases {
		got, err := AmountCentsFromYuan(in)
		if err != nil || got != want {
			t.Fatalf("AmountCentsFromYuan(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}
