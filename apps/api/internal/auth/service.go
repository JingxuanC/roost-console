package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Service signature:
//
//	service \n timestamp \n nonce \n method \n requestURI \n sha256(body)
//
// HMAC-SHA256 with the shared service secret. Max clock skew is 5 minutes and
// nonces are single-use inside that window.
const serviceSkew = 5 * time.Minute

var serviceNonces = struct {
	sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

func SignService(service, method, requestURI string, body []byte, secret string, now time.Time) (string, string, string) {
	ts := strconv.FormatInt(now.Unix(), 10)
	nonce := strconv.FormatInt(now.UnixNano(), 36)
	sum := sha256.Sum256(body)
	input := service + "\n" + ts + "\n" + nonce + "\n" + method + "\n" + requestURI + "\n" + hex.EncodeToString(sum[:])
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return ts, nonce, hex.EncodeToString(mac.Sum(nil))
}

// VerifyServiceSignature checks the request's signed-service headers against
// the request URI exactly as this process received it.
func VerifyServiceSignature(r *http.Request, body []byte, secret string) bool {
	return VerifyServiceSignatureForURI(r, body, secret, r.URL.RequestURI())
}

// VerifyServiceSignatureForURI is VerifyServiceSignature against an explicit
// signing path. A deployment behind a prefix-stripping proxy (nginx
// `location /console-api/`) receives the stripped path, while first-party
// clients sign the public path they called. The caller passes each candidate
// path here so one shared secret accepts both forms.
func VerifyServiceSignatureForURI(r *http.Request, body []byte, secret, requestURI string) bool {
	service := strings.TrimSpace(r.Header.Get("X-Roost-Service"))
	ts := strings.TrimSpace(r.Header.Get("X-Roost-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-Roost-Nonce"))
	sig := strings.TrimSpace(r.Header.Get("X-Roost-Signature"))
	if service == "" || ts == "" || nonce == "" || len(nonce) < 8 || len(nonce) > 128 || sig == "" {
		return false
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if delta := time.Since(time.Unix(unix, 0)); delta > serviceSkew || delta < -serviceSkew {
		return false
	}
	sum := sha256.Sum256(body)
	input := service + "\n" + ts + "\n" + nonce + "\n" + r.Method + "\n" + requestURI + "\n" + hex.EncodeToString(sum[:])
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return false
	}
	now := time.Now()
	serviceNonces.Lock()
	defer serviceNonces.Unlock()
	for k, exp := range serviceNonces.seen {
		if now.After(exp) {
			delete(serviceNonces.seen, k)
		}
	}
	key := service + ":" + nonce
	if _, used := serviceNonces.seen[key]; used {
		return false
	}
	serviceNonces.seen[key] = now.Add(serviceSkew)
	return true
}
