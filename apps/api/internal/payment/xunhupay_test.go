package payment

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestQueryReturnsProviderStatus(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode query body: %v", err)
		}
		received = url.Values{}
		for key, value := range body {
			received.Set(key, value)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errcode": 0,
			"data":    map[string]any{"status": "OD", "open_order_id": "open-1"},
		})
	}))
	t.Cleanup(server.Close)

	client := &Client{AppID: "app-1", AppSecret: "secret", QueryURL: server.URL, HTTP: server.Client()}
	result, err := client.Query(context.Background(), "ORDER1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if result.Status != "OD" {
		t.Fatalf("status = %q, want OD", result.Status)
	}
	if received.Get("out_trade_order") != "ORDER1" || received.Get("appid") != "app-1" {
		t.Fatalf("query params = %v", received)
	}
	if !Verify(received, "secret") {
		t.Fatalf("query request signature invalid: %v", received)
	}
}

func TestQueryRejectsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 500, "errmsg": "invalid sign!"})
	}))
	t.Cleanup(server.Close)
	client := &Client{AppID: "app-1", AppSecret: "secret", QueryURL: server.URL, HTTP: server.Client()}
	if _, err := client.Query(context.Background(), "ORDER1"); err == nil {
		t.Fatal("provider error was ignored")
	}
}
