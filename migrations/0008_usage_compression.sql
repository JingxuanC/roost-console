-- Relay input-compression savings: one row per settlement. The relay reports
-- how many input tokens compression saved, the user's share of those savings,
-- and the cost of generating summaries. The row is written in the same
-- transaction as the settlement and is keyed by the settlement id, so a retried
-- settle never inserts it twice. Rows are the reporting breakdown only; the
-- charge itself stays in billing.ledger.
create table if not exists billing.usage_compression (
  id bigserial primary key,
  org_id uuid not null references public.organizations(id) on delete cascade,
  request_id text not null,
  settlement_id text not null,
  lease_id uuid not null references billing.usage_leases(id) on delete cascade,
  applied boolean not null default false,
  original_input_tokens bigint not null check (original_input_tokens >= 0),
  compressed_input_tokens bigint not null check (compressed_input_tokens >= 0),
  saved_tokens bigint not null check (saved_tokens >= 0),
  user_share_tokens bigint not null check (user_share_tokens >= 0),
  user_share_minor bigint not null check (user_share_minor >= 0),
  summary_cost_minor bigint not null check (summary_cost_minor >= 0),
  created_at timestamptz not null default now(),
  -- One row per settlement. A retried settle hits this unique key and inserts
  -- nothing, so the ledger and the report stay idempotent together.
  unique (settlement_id)
);

-- Organization reporting reads newest first; lease/request lookups serve
-- replay and debugging. The unique settlement key above also serves the
-- idempotent insert.
create index if not exists usage_compression_org_created on billing.usage_compression(org_id, created_at desc);
create index if not exists usage_compression_lease on billing.usage_compression(lease_id);
create index if not exists usage_compression_request on billing.usage_compression(request_id);

alter table billing.usage_compression enable row level security;
revoke all on billing.usage_compression from anon, authenticated;
revoke all on sequence billing.usage_compression_id_seq from anon, authenticated;

-- Application role needs the same access as the other billing tables.
grant select, insert, update, delete on billing.usage_compression to roost_api;
grant usage, select on sequence billing.usage_compression_id_seq to roost_api;
