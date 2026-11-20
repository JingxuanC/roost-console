package payment

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrNotConfigured = errors.New("payment provider is not configured")

type Client struct {
	AppID     string
	AppSecret string
	APIBase   string
	NotifyURL string
	ReturnURL string
	HTTP      *http.Client
}

type CreateInput struct {
	OutTradeNo  string
	AmountCents int
	Title       string
	Attach      string
}

type CreateResult struct {
	ProviderOrderID string
	QRURL           string
	PayURL          string
}

// Sign implements XunhuPay's MD5 rule: non-empty params sorted by ASCII key,
// joined as k=v&..., with the app secret appended directly before md5.
func Sign(values url.Values, secret string) string {
	keys := make([]string, 0, len(values))
	for k, vs := range values {
		if k == "hash" || len(vs) == 0 || vs[0] == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(values.Get(k))
	}
	sum := md5.Sum([]byte(b.String() + secret))
	return hex.EncodeToString(sum[:])
}

func Verify(values url.Values, secret string) bool {
	got := strings.ToLower(strings.TrimSpace(values.Get("hash")))
	if got == "" {
		return false
	}
	want := Sign(values, secret)
	return got == want
}

func (c *Client) Create(in CreateInput) (CreateResult, error) {
	if c.AppID == "" || c.AppSecret == "" {
		return CreateResult{}, ErrNotConfigured
	}
	form := url.Values{}
	form.Set("version", "1.1")
	form.Set("appid", c.AppID)
	form.Set("trade_order_id", in.OutTradeNo)
	form.Set("total_fee", strconv.FormatFloat(float64(in.AmountCents)/100, 'f', 2, 64))
	form.Set("title", in.Title)
	form.Set("time", strconv.FormatInt(time.Now().Unix(), 10))
	form.Set("notify_url", c.NotifyURL)
	if c.ReturnURL != "" {
		form.Set("return_url", c.ReturnURL)
	}
	form.Set("attach", in.Attach)
	form.Set("nonce_str", strings.ReplaceAll(uuid.NewString(), "-", ""))
	form.Set("hash", Sign(form, c.AppSecret))

	body, _ := json.Marshal(formToMap(form))
	req, err := http.NewRequest(http.MethodPost, c.APIBase, bytes.NewReader(body))
	if err != nil {
		return CreateResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return CreateResult{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return CreateResult{}, fmt.Errorf("xunhupay response: %w", err)
	}
	code, _ := out["errcode"].(float64)
	if int(code) != 0 {
		return CreateResult{}, fmt.Errorf("xunhupay: %v %v", out["errcode"], out["errmsg"])
	}
	return CreateResult{
		ProviderOrderID: fmt.Sprint(out["openid"]),
		QRURL:           fmt.Sprint(out["url_qrcode"]),
		PayURL:          fmt.Sprint(out["url"]),
	}, nil
}

func formToMap(v url.Values) map[string]string {
	m := make(map[string]string, len(v))
	for k := range v {
		m[k] = v.Get(k)
	}
	return m
}

func AmountCentsFromYuan(s string) (int, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return int(f*100 + 0.5), nil
}
