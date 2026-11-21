package billing

import (
	"context"
	"encoding/json"
	"time"
)

// End-user billing detail. The per-organization dashboard answers "how many
// tokens and sandboxes did we consume, and what did each cost?". Token counts
// come from billing.usage_events (the relay's per-request report) while spend
// comes from billing.ledger, the source of truth for money. Sandbox spend comes
// from billing.sandbox_usage, the detail row written next to its ledger charge.
//
// Console money convention: 1 credit = 1000 minor units, 100 credits = ¥1, so
// one minor unit is ¥0.00001 and ¥ = minor / 100000.
const (
	BillingDetailsDaysDefault = UsageDaysDefault
	BillingDetailsDaysMax     = UsageDaysMax

	// BillingDetailsSandboxLimit caps the sandbox detail rows in one response.
	// The summary's sandbox_count still counts every sandbox in range, so the
	// frontend can say when the table is showing a truncated page.
	BillingDetailsSandboxLimit = 500
)

// BillingDetailsSummary is the headline aggregate of the range.
type BillingDetailsSummary struct {
	TotalCredits   int64   `json:"total_credits"`
	TotalMinor     int64   `json:"total_minor"`
	TotalYuan      float64 `json:"total_yuan"`
	TokenCredits   int64   `json:"token_credits"`
	TokenMinor     int64   `json:"token_minor"`
	SandboxCredits int64   `json:"sandbox_credits"`
	SandboxMinor   int64   `json:"sandbox_minor"`
	SandboxCount   int64   `json:"sandbox_count"`
	Requests       int64   `json:"requests"`
	TotalTokens    int64   `json:"total_tokens"`
}

// BillingTokenRow is one model's token usage in the range.
//
// CreditsMinor is exact only when every lease that contributed to this model
// settled one model (the normal case). A lease carrying several models has one
// combined ledger charge and no honest per-model split, so its credits fields
// stay null rather than invent a proportional allocation. A ledger charge with
// no usage event (older relay clients) counts in the summary but has no model
// row, so the per-model credits need not add up to the summary token spend.
//
// The ledger stores a single settled amount per lease, with no input/output
// price split. The effective_input_* and effective_output_* rates are therefore
// null until the relay reports that split; the UI can still show a blended
// credits-per-million-token rate from CreditsMinor and TotalTokens.
type BillingTokenRow struct {
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TotalTokens      int64  `json:"total_tokens"`

	CreditsMinor *int64   `json:"credits_minor"`
	Credits      *int64   `json:"credits"`
	Yuan         *float64 `json:"yuan"`

	EffectiveInputCreditsPerMillion  *float64 `json:"effective_input_credits_per_million"`
	EffectiveOutputCreditsPerMillion *float64 `json:"effective_output_credits_per_million"`
}

// BillingSandboxRow is one sandbox's measured usage and charge.
type BillingSandboxRow struct {
	ID                int64           `json:"id"`
	SandboxID         string          `json:"sandbox_id"`
	Provider          string          `json:"provider"`
	ProviderSandboxID string          `json:"provider_sandbox_id"`
	UserID            string          `json:"user_id"`
	Product           string          `json:"product"`
	StartedAt         *time.Time      `json:"started_at"`
	EndedAt           *time.Time      `json:"ended_at"`
	BillableSeconds   int64           `json:"billable_seconds"`
	BillableHours     float64         `json:"billable_hours"`
	CPUSeconds        int64           `json:"cpu_seconds"`
	MemoryMiBSeconds  int64           `json:"memory_mib_seconds"`
	DiskMiBSeconds    int64           `json:"disk_mib_seconds"`
	GPUSeconds        int64           `json:"gpu_seconds"`
	CPUCredits        float64         `json:"cpu_credits"`
	MemoryCredits     float64         `json:"memory_credits"`
	DiskCredits       float64         `json:"disk_credits"`
	GPUCredits        float64         `json:"gpu_credits"`
	TotalCredits      int64           `json:"total_credits"`
	CreditsMinor      int64           `json:"credits_minor"`
	Yuan              float64         `json:"yuan"`
	UnitPrices        json.RawMessage `json:"unit_prices"`
}

// BillingDetails is the GET /v1/orgs/{orgID}/billing/details payload.
type BillingDetails struct {
	Days       int                   `json:"days"`
	RangeStart time.Time             `json:"range_start"`
	RangeEnd   time.Time             `json:"range_end"`
	Summary    BillingDetailsSummary `json:"summary"`
	Tokens     []BillingTokenRow     `json:"tokens"`
	Sandboxes  []BillingSandboxRow   `json:"sandboxes"`
}

func billingDetailsYuan(minor int64) float64 {
	return float64(minor) / float64(100000)
}

func billingDetailsRange(days int, now time.Time) (time.Time, time.Time) {
	end := now.UTC()
	start := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -(days - 1))
	return start, end
}

// BillingDetails aggregates one organization's token and sandbox charges for
// the trailing N UTC calendar days. Every query is scoped by org_id. The token
// spend query deliberately selects only the token products and the two usage
// reasons, so sandbox charges (reason='sandbox_usage'), grants, payments and
// admin adjustments never leak into the token total.
func (s *Store) BillingDetails(ctx context.Context, orgID string, days int) (BillingDetails, error) {
	days = clampUsageDays(days)
	start, end := billingDetailsRange(days, time.Now())
	out := BillingDetails{
		Days:       days,
		RangeStart: start,
		RangeEnd:   end,
		Tokens:     make([]BillingTokenRow, 0),
		Sandboxes:  make([]BillingSandboxRow, 0),
	}

	// Token counts: the relay's per-request usage report.
	var reqs, input, output, cacheRead, cacheWrite int64
	if err := s.Pool.QueryRow(ctx, `
		select coalesce(sum(requests),0)::bigint,
		       coalesce(sum(input_tokens),0)::bigint,
		       coalesce(sum(output_tokens),0)::bigint,
		       coalesce(sum(cache_read_tokens),0)::bigint,
		       coalesce(sum(cache_write_tokens),0)::bigint
		from billing.usage_events
		where org_id=$1 and created_at >= `+usageSince, orgID, days).
		Scan(&reqs, &input, &output, &cacheRead, &cacheWrite); err != nil {
		return BillingDetails{}, err
	}
	out.Summary.Requests = reqs
	out.Summary.TotalTokens = input + output + cacheRead + cacheWrite

	// Token spend: usage charges only, for the token products. product/ref_id
	// are not null in practice, but coalesce keeps the sum robust.
	var tokenCredits int64
	if err := s.Pool.QueryRow(ctx, `
		select coalesce(sum(abs(delta_credits)),0)::bigint
		from billing.ledger
		where org_id=$1 and created_at >= `+usageSince+`
		  and delta_credits < 0
		  and reason in ('usage','relay_usage')
		  and product in ('relay','dslpha','causal-memory')`, orgID, days).
		Scan(&tokenCredits); err != nil {
		return BillingDetails{}, err
	}
	out.Summary.TokenCredits = tokenCredits
	out.Summary.TokenMinor = tokenCredits * SandboxCreditsMinorPerCredit

	// Sandbox spend: the detail rows written with each sandbox charge.
	var sandboxCredits, sandboxMinor, sandboxCount int64
	if err := s.Pool.QueryRow(ctx, `
		select coalesce(sum(total_credits),0)::bigint,
		       coalesce(sum(credits_minor),0)::bigint,
		       count(*)::bigint
		from billing.sandbox_usage
		where org_id=$1 and created_at >= `+usageSince, orgID, days).
		Scan(&sandboxCredits, &sandboxMinor, &sandboxCount); err != nil {
		return BillingDetails{}, err
	}
	out.Summary.SandboxCredits = sandboxCredits
	out.Summary.SandboxMinor = sandboxMinor
	out.Summary.SandboxCount = sandboxCount
	out.Summary.TotalCredits = tokenCredits + sandboxCredits
	out.Summary.TotalMinor = out.Summary.TokenMinor + out.Summary.SandboxMinor
	out.Summary.TotalYuan = billingDetailsYuan(out.Summary.TotalMinor)

	// Per-model token breakdown with exact credits where the lease carried a
	// single model. per_lease.models counts distinct models per lease; a lease
	// with more than one model marks the model's credits as null instead of
	// splitting its combined charge by an invented ratio.
	modelRows, err := s.Pool.Query(ctx, `
		with ev as (
			select model, lease_id,
			       coalesce(sum(requests),0)::bigint as requests,
			       coalesce(sum(input_tokens),0)::bigint as input_tokens,
			       coalesce(sum(output_tokens),0)::bigint as output_tokens,
			       coalesce(sum(cache_read_tokens),0)::bigint as cache_read_tokens,
			       coalesce(sum(cache_write_tokens),0)::bigint as cache_write_tokens
			from billing.usage_events
			where org_id=$1 and created_at >= `+usageSince+`
			group by model, lease_id
		), per_model as (
			select model,
			       sum(requests)::bigint as requests,
			       sum(input_tokens)::bigint as input_tokens,
			       sum(output_tokens)::bigint as output_tokens,
			       sum(cache_read_tokens)::bigint as cache_read_tokens,
			       sum(cache_write_tokens)::bigint as cache_write_tokens
			from ev group by model
		), per_lease as (
			select lease_id, count(distinct model)::bigint as models
			from ev group by lease_id
		), per_lease_credits as (
			select l.ref_id as lease_id, coalesce(sum(abs(l.delta_credits)),0)::bigint as credits
			from billing.ledger l
			join per_lease pl on pl.lease_id::text = l.ref_id
			where l.org_id=$1 and l.created_at >= `+usageSince+`
			  and l.delta_credits < 0
			  and l.reason in ('usage','relay_usage')
			  and l.product in ('relay','dslpha','causal-memory')
			group by l.ref_id
		), attribution as (
			select ev.model,
			       coalesce(sum(case when pl.models=1 then coalesce(plc.credits,0) else 0 end),0)::bigint as exact_credits,
			       coalesce(bool_or(pl.models>1),false) as multi_model_lease
			from ev
			join per_lease pl on pl.lease_id=ev.lease_id
			left join per_lease_credits plc on plc.lease_id=ev.lease_id::text
			group by ev.model
		)
		select pm.model, pm.requests, pm.input_tokens, pm.output_tokens,
		       pm.cache_read_tokens, pm.cache_write_tokens,
		       (pm.input_tokens+pm.output_tokens+pm.cache_read_tokens+pm.cache_write_tokens)::bigint as total_tokens,
		       case when a.multi_model_lease then null else a.exact_credits*1000 end as credits_minor
		from per_model pm
		join attribution a on a.model=pm.model
		order by total_tokens desc, pm.model asc`, orgID, days)
	if err != nil {
		return BillingDetails{}, err
	}
	for modelRows.Next() {
		var row BillingTokenRow
		if err := modelRows.Scan(&row.Model, &row.Requests, &row.InputTokens, &row.OutputTokens,
			&row.CacheReadTokens, &row.CacheWriteTokens, &row.TotalTokens, &row.CreditsMinor); err != nil {
			modelRows.Close()
			return BillingDetails{}, err
		}
		if row.CreditsMinor != nil {
			credits := *row.CreditsMinor / SandboxCreditsMinorPerCredit
			yuan := billingDetailsYuan(*row.CreditsMinor)
			row.Credits = &credits
			row.Yuan = &yuan
		}
		out.Tokens = append(out.Tokens, row)
	}
	if err := modelRows.Err(); err != nil {
		modelRows.Close()
		return BillingDetails{}, err
	}
	modelRows.Close()

	sandboxRows, err := s.Pool.Query(ctx, `
		select id, sandbox_id, coalesce(provider,''), coalesce(provider_sandbox_id,''),
		       coalesce(user_id,''), product, started_at, ended_at, billable_seconds,
		       cpu_seconds, memory_mib_seconds, disk_mib_seconds, gpu_seconds,
		       cpu_credits::float8, memory_credits::float8, disk_credits::float8,
		       gpu_credits::float8, total_credits, credits_minor,
		       coalesce(unit_prices::text,'')
		from billing.sandbox_usage
		where org_id=$1 and created_at >= `+usageSince+`
		order by coalesce(started_at, created_at) desc, id desc
		limit $3`, orgID, days, BillingDetailsSandboxLimit)
	if err != nil {
		return BillingDetails{}, err
	}
	for sandboxRows.Next() {
		var row BillingSandboxRow
		var unitPrices string
		if err := sandboxRows.Scan(&row.ID, &row.SandboxID, &row.Provider, &row.ProviderSandboxID,
			&row.UserID, &row.Product, &row.StartedAt, &row.EndedAt, &row.BillableSeconds,
			&row.CPUSeconds, &row.MemoryMiBSeconds, &row.DiskMiBSeconds, &row.GPUSeconds,
			&row.CPUCredits, &row.MemoryCredits, &row.DiskCredits, &row.GPUCredits,
			&row.TotalCredits, &row.CreditsMinor, &unitPrices); err != nil {
			sandboxRows.Close()
			return BillingDetails{}, err
		}
		row.BillableHours = float64(row.BillableSeconds) / 3600
		row.Yuan = billingDetailsYuan(row.CreditsMinor)
		if unitPrices != "" {
			row.UnitPrices = json.RawMessage(unitPrices)
		}
		out.Sandboxes = append(out.Sandboxes, row)
	}
	if err := sandboxRows.Err(); err != nil {
		sandboxRows.Close()
		return BillingDetails{}, err
	}
	sandboxRows.Close()

	return out, nil
}
