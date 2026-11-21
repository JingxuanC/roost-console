package billing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Tool-charge breakdown for the relay's Phase 2 settlement contract. The relay
// sends only successful calls; the console stores one row per call and keeps the
// settlement total itself in billing.ledger.
const (
	MaxToolCharges            = 100
	maxToolChargeToolLen      = 200
	maxToolChargeUnitLen      = 32
	ToolChargeStatusSucceeded = "succeeded"
)

// ErrInvalidToolCharges is returned for a malformed tool_charges array. The
// HTTP layer maps it to 400 before a settlement is booked.
var ErrInvalidToolCharges = errors.New("invalid tool_charges")

var toolChargeUnits = map[string]struct{}{
	"call":      {},
	"page":      {},
	"second":    {},
	"character": {},
}

// ToolCharge is one successful tool call in a settlement breakdown. AmountMinor
// is the charge in the relay's minor unit (1000 micro-credit); UnitPriceMicro is
// the quoted price in micro-credit; ProviderCostMicro is the upstream cost used
// for margin reporting.
type ToolCharge struct {
	Tool              string  `json:"tool"`
	Unit              string  `json:"unit"`
	Quantity          float64 `json:"quantity"`
	UnitPriceMicro    int64   `json:"unit_price_micro"`
	AmountMinor       int64   `json:"amount_minor"`
	ProviderCostMicro int64   `json:"provider_cost_micro"`
	Status            string  `json:"status"`
}

// ToolChargeTotals is the aggregate of a settlement's tool-charge breakdown.
type ToolChargeTotals struct {
	AmountMinor       int64 `json:"amount_minor"`
	ProviderCostMicro int64 `json:"provider_cost_micro"`
}

// NormalizeToolCharges validates the array and trims/lowercases the fields that
// the contract defines as lowercase tokens. It is called by the HTTP handler
// before the store so malformed input is rejected without booking anything, and
// again by the store as defense in depth.
func NormalizeToolCharges(charges []ToolCharge) ([]ToolCharge, error) {
	if len(charges) == 0 {
		return nil, nil
	}
	if len(charges) > MaxToolCharges {
		return nil, fmt.Errorf("%w: at most %d entries are allowed", ErrInvalidToolCharges, MaxToolCharges)
	}
	out := make([]ToolCharge, len(charges))
	for i, c := range charges {
		c.Tool = strings.TrimSpace(c.Tool)
		c.Unit = strings.ToLower(strings.TrimSpace(c.Unit))
		c.Status = strings.ToLower(strings.TrimSpace(c.Status))
		switch {
		case c.Tool == "":
			return nil, fmt.Errorf("%w: tool_charges[%d].tool is required", ErrInvalidToolCharges, i)
		case len(c.Tool) > maxToolChargeToolLen:
			return nil, fmt.Errorf("%w: tool_charges[%d].tool is too long", ErrInvalidToolCharges, i)
		case c.Unit == "":
			return nil, fmt.Errorf("%w: tool_charges[%d].unit is required", ErrInvalidToolCharges, i)
		case len(c.Unit) > maxToolChargeUnitLen:
			return nil, fmt.Errorf("%w: tool_charges[%d].unit is too long", ErrInvalidToolCharges, i)
		case !knownToolChargeUnit(c.Unit):
			return nil, fmt.Errorf("%w: tool_charges[%d].unit is not supported", ErrInvalidToolCharges, i)
		case c.Status != ToolChargeStatusSucceeded:
			return nil, fmt.Errorf("%w: tool_charges[%d].status must be %q", ErrInvalidToolCharges, i, ToolChargeStatusSucceeded)
		case c.Quantity <= 0 || math.IsNaN(c.Quantity) || math.IsInf(c.Quantity, 0):
			return nil, fmt.Errorf("%w: tool_charges[%d].quantity must be > 0", ErrInvalidToolCharges, i)
		case c.UnitPriceMicro < 0:
			return nil, fmt.Errorf("%w: tool_charges[%d].unit_price_micro must be >= 0", ErrInvalidToolCharges, i)
		case c.AmountMinor < 0:
			return nil, fmt.Errorf("%w: tool_charges[%d].amount_minor must be >= 0", ErrInvalidToolCharges, i)
		case c.ProviderCostMicro < 0:
			return nil, fmt.Errorf("%w: tool_charges[%d].provider_cost_micro must be >= 0", ErrInvalidToolCharges, i)
		}
		out[i] = c
	}
	return out, nil
}

func knownToolChargeUnit(unit string) bool {
	_, ok := toolChargeUnits[unit]
	return ok
}

// SumToolCharges returns the breakdown's totals. It is used for the API response
// and the admin billing view.
func SumToolCharges(charges []ToolCharge) ToolChargeTotals {
	var totals ToolChargeTotals
	for _, c := range charges {
		totals.AmountMinor += c.AmountMinor
		totals.ProviderCostMicro += c.ProviderCostMicro
	}
	return totals
}

type toolChargeQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// queryToolCharges loads a settlement's breakdown by lease. A lease settles at
// most once, so this is also the idempotent replay read path.
func queryToolCharges(ctx context.Context, q toolChargeQuerier, leaseID string) ([]ToolCharge, error) {
	rows, err := q.Query(ctx, `
		select tool,unit,quantity,unit_price_micro,amount_minor,provider_cost_micro,status
		from billing.usage_tool_charges
		where lease_id=$1
		order by id`, leaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ToolCharge, 0)
	for rows.Next() {
		var c ToolCharge
		if err := rows.Scan(&c.Tool, &c.Unit, &c.Quantity, &c.UnitPriceMicro, &c.AmountMinor, &c.ProviderCostMicro, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// insertToolCharges writes the breakdown inside the settlement transaction.
// The unnest form keeps it to one round trip even when the relay reports many
// tool calls. An empty breakdown is a no-op, preserving pre-Phase-2 behavior.
func insertToolCharges(ctx context.Context, tx pgx.Tx, orgID, requestID, settlementID, leaseID string, charges []ToolCharge) error {
	if len(charges) == 0 {
		return nil
	}
	tools := make([]string, len(charges))
	units := make([]string, len(charges))
	quantities := make([]float64, len(charges))
	unitPrices := make([]int64, len(charges))
	amounts := make([]int64, len(charges))
	providerCosts := make([]int64, len(charges))
	statuses := make([]string, len(charges))
	for i, c := range charges {
		tools[i] = c.Tool
		units[i] = c.Unit
		quantities[i] = c.Quantity
		unitPrices[i] = c.UnitPriceMicro
		amounts[i] = c.AmountMinor
		providerCosts[i] = c.ProviderCostMicro
		statuses[i] = c.Status
	}
	_, err := tx.Exec(ctx, `
		insert into billing.usage_tool_charges
			(org_id,request_id,settlement_id,lease_id,tool,unit,quantity,unit_price_micro,amount_minor,provider_cost_micro,status)
		select $1,$2,$3,$4,t.tool,t.unit,t.quantity,t.unit_price_micro,t.amount_minor,t.provider_cost_micro,t.status
		from unnest($5::text[],$6::text[],$7::float8[],$8::bigint[],$9::bigint[],$10::bigint[],$11::text[])
			as t(tool,unit,quantity,unit_price_micro,amount_minor,provider_cost_micro,status)`,
		orgID, requestID, settlementID, leaseID,
		tools, units, quantities, unitPrices, amounts, providerCosts, statuses)
	return err
}
