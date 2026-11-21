package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
)

func validCompressionPayload() billing.Compression {
	return billing.Compression{
		Applied:               true,
		OriginalInputTokens:   33404,
		CompressedInputTokens: 7378,
		SavedTokens:           26026,
		UserShareTokens:       7807,
		UserShareMinor:        1542,
		SummaryCostMinor:      0,
	}
}

func TestRelaySettleWithCompression(t *testing.T) {
	f := &fakeRelayBilling{settleOp: billing.RelayOperation{
		Found: true, Operation: "settle", Status: "settled", ReservationID: "res-1", Amount: 1,
	}}
	rec := postRelaySettle(t, relayTestServer(f), `{
		"settlement_id":"stl-1",
		"idempotency_key":"settle:req-1",
		"amount_minor":1000,
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
	if len(f.settleCalls) != 1 || f.settleCalls[0].compression == nil {
		t.Fatalf("settle calls=%+v", f.settleCalls)
	}
	want := validCompressionPayload()
	if got := *f.settleCalls[0].compression; got != want {
		t.Fatalf("compression=%+v want=%+v", got, want)
	}
	body := decodeRelayResponse(t, rec)
	compression, ok := body["compression"].(map[string]any)
	if !ok {
		t.Fatalf("response compression=%v", body["compression"])
	}
	if compression["applied"] != true || compression["saved_tokens"] != float64(26026) ||
		compression["user_share_minor"] != float64(1542) {
		t.Fatalf("response compression=%v", compression)
	}
}

func TestRelaySettleCompressionZeroOrAbsentStoresNothing(t *testing.T) {
	cases := map[string]string{
		"absent": `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000}`,
		"zero": `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000,
			"compression":{"applied":true,"original_input_tokens":0,"compressed_input_tokens":0,
			"saved_tokens":0,"user_share_tokens":0,"user_share_minor":0,"summary_cost_minor":0}}`,
		"not applied with zero counters": `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000,
			"compression":{"applied":false}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeRelayBilling{settleOp: billing.RelayOperation{Found: true, Operation: "settle", Status: "settled", Amount: 1}}
			rec := postRelaySettle(t, relayTestServer(f), payload)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(f.settleCalls) != 1 {
				t.Fatalf("settle calls=%d", len(f.settleCalls))
			}
			if f.settleCalls[0].compression != nil {
				t.Fatalf("zero/absent compression reached the store: %+v", *f.settleCalls[0].compression)
			}
			body := decodeRelayResponse(t, rec)
			if body["compression"] != nil {
				t.Fatalf("response compression=%v want null", body["compression"])
			}
		})
	}
}

func TestRelaySettleRejectsNegativeCompression(t *testing.T) {
	cases := map[string]string{
		"original":     `"applied":true,"original_input_tokens":-1,"compressed_input_tokens":7378,"saved_tokens":26026,"user_share_tokens":7807,"user_share_minor":1542,"summary_cost_minor":0`,
		"compressed":   `"applied":true,"original_input_tokens":33404,"compressed_input_tokens":-1,"saved_tokens":26026,"user_share_tokens":7807,"user_share_minor":1542,"summary_cost_minor":0`,
		"saved":        `"applied":true,"original_input_tokens":33404,"compressed_input_tokens":7378,"saved_tokens":-1,"user_share_tokens":7807,"user_share_minor":1542,"summary_cost_minor":0`,
		"share tokens": `"applied":true,"original_input_tokens":33404,"compressed_input_tokens":7378,"saved_tokens":26026,"user_share_tokens":-1,"user_share_minor":1542,"summary_cost_minor":0`,
		"share minor":  `"applied":true,"original_input_tokens":33404,"compressed_input_tokens":7378,"saved_tokens":26026,"user_share_tokens":7807,"user_share_minor":-1,"summary_cost_minor":0`,
		"summary cost": `"applied":true,"original_input_tokens":33404,"compressed_input_tokens":7378,"saved_tokens":26026,"user_share_tokens":7807,"user_share_minor":1542,"summary_cost_minor":-1`,
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeRelayBilling{}
			rec := postRelaySettle(t, relayTestServer(f), fmt.Sprintf(
				`{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000,"compression":{%s}}`, fields))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeRelayResponse(t, rec)
			errBody, _ := body["error"].(map[string]any)
			if errBody["code"] != "invalid_compression" {
				t.Fatalf("error=%v", body)
			}
			if len(f.settleCalls) != 0 {
				t.Fatalf("malformed compression reached the store: %+v", f.settleCalls)
			}
		})
	}
}

func TestRelaySettleCompressionAppliedMustBeBoolean(t *testing.T) {
	f := &fakeRelayBilling{}
	rec := postRelaySettle(t, relayTestServer(f), `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000,
		"compression":{"applied":"yes","saved_tokens":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeRelayResponse(t, rec)
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "bad_request" {
		t.Fatalf("error=%v", body)
	}
	if len(f.settleCalls) != 0 {
		t.Fatalf("malformed compression reached the store: %+v", f.settleCalls)
	}
}

func TestRelaySettleStoreInvalidCompressionMapsTo400(t *testing.T) {
	f := &fakeRelayBilling{settleErr: fmt.Errorf("%w: saved_tokens", billing.ErrInvalidCompression)}
	rec := postRelaySettle(t, relayTestServer(f), `{"settlement_id":"stl-1","idempotency_key":"settle:req-1","amount_minor":1000}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeRelayResponse(t, rec)
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "invalid_compression" {
		t.Fatalf("error=%v", body)
	}
}
