-- Relay usage analytics: one row per settled request carrying the model and
-- token/cost counters the relay reports. The row is written in the same
-- transaction as the settlement and is keyed by the settlement id, so a retried
-- settle never inserts it twice. Charges stay in billing.ledger; this table is
-- the reporting detail behind the console Usage dashboard. Compression savings
-- remain in billing.usage_compression and are joined by settlement_id.
create table if not exists billing.usage_events (
  id bigserial primary key,
  org_id uuid not null references public.organizations(id) on delete cascade,
  request_id text not null,
  settlement_id text not null,
  lease_id uuid not null references billing.usage_leases(id) on delete cascade,
  model text not null default '' check (length(btrim(model)) <= 200),
  requests bigint not null default 1 check (requests >= 0),
  input_tokens bigint not null default 0 check (input_tokens >= 0),
  output_tokens bigint not null default 0 check (output_tokens >= 0),
  cache_read_tokens bigint not null default 0 check (cache_read_tokens >= 0),
  cache_write_tokens bigint not null default 0 check (cache_write_tokens >= 0),
  upstream_cost_minor bigint check (upstream_cost_minor is null or upstream_cost_minor >= 0),
  created_at timestamptz not null default now(),
  -- One row per settlement. A retried settle hits this unique key and inserts
  -- nothing, so the ledger and the usage report stay idempotent together.
  unique (settlement_id)
);

-- Organization reporting reads newest first; the model index serves the model
-- activity breakdown. The unique settlement key above is the idempotent insert
-- path and doubles as the settlement lookup index.
create index if not exists usage_events_org_created on billing.usage_events(org_id, created_at desc);
create index if not exists usage_events_model on billing.usage_events(model);

alter table billing.usage_events enable row level security;
revoke all on billing.usage_events from anon, authenticated;
revoke all on sequence billing.usage_events_id_seq from anon, authenticated;

-- Application role needs the same access as the other billing tables.
grant select, insert, update, delete on billing.usage_events to roost_api;
grant usage, select on sequence billing.usage_events_id_seq to roost_api;
