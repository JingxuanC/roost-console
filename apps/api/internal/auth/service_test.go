package auth

import (
	"net/http"
	"testing"
	"time"
)

func signedRequest(body []byte, secret string, now time.Time) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "https://api.example/internal/v1/usage/charge?x=1", nil)
	ts, nonce, sig := SignService("market", r.Method, r.URL.RequestURI(), body, secret, now)
	r.Header.Set("X-Roost-Service", "market")
	r.Header.Set("X-Roost-Timestamp", ts)
	r.Header.Set("X-Roost-Nonce", nonce)
	r.Header.Set("X-Roost-Signature", sig)
	return r
}

func TestServiceSignature(t *testing.T) {
	body := []byte(`{"org_id":"o"}`)
	r := signedRequest(body, "secret", time.Now())
	if !VerifyServiceSignature(r, body, "secret") { t.Fatal("valid signature rejected") }
	if VerifyServiceSignature(r, body, "other") { t.Fatal("wrong secret accepted") }
}

func TestServiceSignatureTamper(t *testing.T) {
	body := []byte(`{"credits":1}`)
	r := signedRequest(body, "secret", time.Now())
	if VerifyServiceSignature(r, []byte(`{"credits":999}`), "secret") { t.Fatal("body tamper accepted") }
	r.URL.Path = "/other"
	if VerifyServiceSignature(r, body, "secret") { t.Fatal("path tamper accepted") }
}

func TestServiceSignatureReplay(t *testing.T) {
	body := []byte(`{}`)
	r := signedRequest(body, "secret", time.Now())
	if !VerifyServiceSignature(r, body, "secret") { t.Fatal("first rejected") }
	if VerifyServiceSignature(r, body, "secret") { t.Fatal("replay accepted") }
}

func TestServiceSignatureSkew(t *testing.T) {
	body := []byte(`{}`)
	r := signedRequest(body, "secret", time.Now().Add(-10*time.Minute))
	if VerifyServiceSignature(r, body, "secret") { t.Fatal("stale timestamp accepted") }
}
