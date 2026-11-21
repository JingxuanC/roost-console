package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

func TestRelaySettleWithUsage(t *testing.T) {
	f := &fakeRelayBilling{settleOp: billing.RelayOperation{
		Found: true, Operation: "settle", Status: "settled", ReservationID: "res-1", Amount: 2,
	}}
	rec := postRelaySettle(t, relayTestServer(f), `{
		"settlement_id":"stl-1",
		"idempotency_key":"settle:req-1",
		"amount_minor":2000,
		"usage":{
			"model":" deepseek-v4-pro ",
			"requests":1,
			"input_tokens":33404,
			"output_tokens":256,
			"cache_read_tokens":8192,
			"cache_write_tokens":0,
			"upstream_cost_minor":1616
		},
		"compression":{
			"applied":true,
			"original_input_tokens":33404,
			"compressed_input_tokens":7378,
			"saved_tokens":26026,
			"user_share_tokens":7807,
			"user_share_minor":1542,
			"summary_cost_minor":0
		}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(f.settleCalls) != 1 || f.settleCalls[0].usage == nil {
		t.Fatalf("settle calls=%+v", f.settleCalls)
	}
	usage := f.settleCalls[0].usage
	if usage.Model != "deepseek-v4-pro" || usage.Requests != 1 || usage.InputTokens != 33404 ||
		usage.OutputTokens != 256 || usage.CacheReadTokens != 8192 || usage.CacheWriteTokens != 0 ||
		usage.UpstreamCostMinor == nil || *usage.UpstreamCostMinor != 1616 {
		t.Fatalf("usage=%+v", usage)
	}
	// Compression stays a separate object on the same settle.
	if f.settleCalls[0].compression == nil || f.settleCalls[0].compression.SavedTokens != 26026 {
		t.Fatalf("compression=%+v", f.settleCalls[0].compression)
	}
	body := decodeRelayResponse(t, rec)
	got, ok := body["usage"].(map[string]any)
	if !ok {
		t.Fatalf("response usage=%v", body["usage"])
	}
	if got["model"] != "deepseek-v4-pro" || got["input_tokens"] != float64(33404) ||
		got["upstream_cost_minor"] != float64(1616) {
		t.Fatalf("response usage=%v", got)
	}
}

func TestRelaySettleUsageAbsentOrZeroStoresNothing(t *testing.T) {
	cases := map[string]string{
		"absent": `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":2000}`,
		"null":   `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":2000,"usage":null}`,
		"zero": `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":2000,
			"usage":{"model":"deepseek-v4-pro","requests":0,"input_tokens":0,"output_tokens":0,
			"cache_read_tokens":0,"cache_write_tokens":0,"upstream_cost_minor":0}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeRelayBilling{settleOp: billing.RelayOperation{Found: true, Operation: "settle", Status: "settled", Amount: 2}}
			rec := postRelaySettle(t, relayTestServer(f), payload)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(f.settleCalls) != 1 {
				t.Fatalf("settle calls=%d", len(f.settleCalls))
			}
			if f.settleCalls[0].usage != nil {
				t.Fatalf("zero/absent usage reached the store: %+v", *f.settleCalls[0].usage)
			}
			body := decodeRelayResponse(t, rec)
			if body["usage"] != nil {
				t.Fatalf("response usage=%v want null", body["usage"])
			}
		})
	}
}

func TestRelaySettleRejectsInvalidUsage(t *testing.T) {
	cases := map[string]string{
		"negative requests":    `"requests":-1`,
		"negative input":       `"input_tokens":-1`,
		"negative output":      `"output_tokens":-1`,
		"negative cache read":  `"cache_read_tokens":-1`,
		"negative cache write": `"cache_write_tokens":-1`,
		"negative cost":        `"upstream_cost_minor":-1`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeRelayBilling{}
			rec := postRelaySettle(t, relayTestServer(f), fmt.Sprintf(
				`{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":2000,"usage":{"model":"deepseek-v4-pro",%s}}`, field))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeRelayResponse(t, rec)
			errBody, _ := body["error"].(map[string]any)
			if errBody["code"] != "invalid_usage" {
				t.Fatalf("error=%v", body)
			}
			if len(f.settleCalls) != 0 {
				t.Fatalf("malformed usage reached the store: %+v", f.settleCalls)
			}
		})
	}
}

func TestRelaySettleRejectsLongModel(t *testing.T) {
	f := &fakeRelayBilling{}
	rec := postRelaySettle(t, relayTestServer(f), fmt.Sprintf(
		`{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":2000,"usage":{"model":%q,"requests":1}}`,
		strings.Repeat("m", billing.UsageEventMaxModelLen+1)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeRelayResponse(t, rec)
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "invalid_usage" {
		t.Fatalf("error=%v", body)
	}
	if len(f.settleCalls) != 0 {
		t.Fatalf("long model reached the store: %+v", f.settleCalls)
	}
}

func TestRelaySettleUsageDefaultsRequests(t *testing.T) {
	f := &fakeRelayBilling{settleOp: billing.RelayOperation{Found: true, Operation: "settle", Status: "settled", Amount: 1}}
	rec := postRelaySettle(t, relayTestServer(f), `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000,
		"usage":{"model":"deepseek-v4-pro","input_tokens":10}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(f.settleCalls) != 1 || f.settleCalls[0].usage == nil || f.settleCalls[0].usage.Requests != 1 {
		t.Fatalf("settle calls=%+v", f.settleCalls)
	}
}

func TestRelaySettleStoreInvalidUsageMapsTo400(t *testing.T) {
	f := &fakeRelayBilling{settleErr: fmt.Errorf("%w: input_tokens", billing.ErrInvalidUsage)}
	rec := postRelaySettle(t, relayTestServer(f), `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeRelayResponse(t, rec)
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "invalid_usage" {
		t.Fatalf("error=%v", body)
	}
}
