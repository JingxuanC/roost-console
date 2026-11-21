-- Phase 2 tool-call billing: the relay settles a request's token charge plus
-- per-tool charges. The settlement total stays in billing.ledger; these rows are
-- the reporting breakdown, one row per successful tool call. They are written in
-- the same transaction as the settlement, so a replay (ledger idempotency key)
-- never inserts them twice.
create table if not exists billing.usage_tool_charges (
  id bigserial primary key,
  org_id uuid not null references public.organizations(id) on delete cascade,
  request_id text not null,
  settlement_id text not null,
  lease_id uuid not null references billing.usage_leases(id) on delete cascade,
  tool text not null check (length(btrim(tool)) > 0),
  unit text not null check (length(btrim(unit)) > 0),
  quantity double precision not null check (quantity > 0),
  unit_price_micro bigint not null check (unit_price_micro >= 0),
  amount_minor bigint not null check (amount_minor >= 0),
  provider_cost_micro bigint not null check (provider_cost_micro >= 0),
  status text not null check (status in ('succeeded')),
  created_at timestamptz not null default now()
);

-- Replay reads the breakdown by the reservation's lease; the other indexes are
-- for request/settlement lookup and organization-level reporting.
create index if not exists usage_tool_charges_request on billing.usage_tool_charges(request_id);
create index if not exists usage_tool_charges_settlement on billing.usage_tool_charges(settlement_id);
create index if not exists usage_tool_charges_lease on billing.usage_tool_charges(lease_id);
create index if not exists usage_tool_charges_org_created on billing.usage_tool_charges(org_id, created_at desc);
create index if not exists usage_tool_charges_tool on billing.usage_tool_charges(tool);

alter table billing.usage_tool_charges enable row level security;
revoke all on billing.usage_tool_charges from anon, authenticated;
revoke all on sequence billing.usage_tool_charges_id_seq from anon, authenticated;

-- Application role needs the same access as the other billing tables.
grant select, insert, update, delete on billing.usage_tool_charges to roost_api;
grant usage, select on sequence billing.usage_tool_charges_id_seq to roost_api;
