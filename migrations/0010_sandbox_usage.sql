-- Sandbox usage: one row per billed logical sandbox run. The row is the
-- user-facing billing detail behind the console's 扣费明细 page; the charge
-- itself stays in billing.ledger (reason='sandbox_usage'), written in the same
-- transaction as this row. Console convention: 1 credit = 1000 minor units and
-- 100 credits = ¥1, so credits_minor = total_credits * 1000.
--
-- A logical sandbox may outlive one provider sandbox (a restart or migration
-- keeps the same sandbox_id but a new provider_sandbox_id). The idempotency key
-- is supplied by the product and is unique, so a retried report is a no-op
-- rather than a second charge.
create table if not exists billing.sandbox_usage (
  id bigserial primary key,
  org_id uuid not null references public.organizations(id) on delete cascade,
  user_id text, -- external product user id (e.g. Supabase user id), nullable
  product text not null, -- dslpha / market
  sandbox_id text not null, -- logical sandbox id, stable across provider restarts
  provider text, -- daytona / e2b
  provider_sandbox_id text,
  run_id text,
  started_at timestamptz,
  ended_at timestamptz,
  billable_seconds bigint not null default 0 check (billable_seconds >= 0),
  cpu_seconds bigint not null default 0 check (cpu_seconds >= 0),
  memory_mib_seconds bigint not null default 0 check (memory_mib_seconds >= 0),
  disk_mib_seconds bigint not null default 0 check (disk_mib_seconds >= 0),
  gpu_seconds bigint not null default 0 check (gpu_seconds >= 0),
  cpu_credits numeric(14,4) not null default 0 check (cpu_credits >= 0),
  memory_credits numeric(14,4) not null default 0 check (memory_credits >= 0),
  disk_credits numeric(14,4) not null default 0 check (disk_credits >= 0),
  gpu_credits numeric(14,4) not null default 0 check (gpu_credits >= 0),
  total_credits bigint not null default 0 check (total_credits >= 0),
  credits_minor bigint not null default 0 check (credits_minor >= 0), -- total_credits * 1000
  currency text not null default 'CNY',
  unit_prices jsonb, -- provider/unit price snapshot used for the charge
  idempotency_key text not null unique,
  created_at timestamptz not null default now()
);

-- Organization reporting reads newest first; sandbox lookups within an
-- organization serve detail drill-down. The unique idempotency key above is the
-- idempotent insert path and doubles as the replay lookup index.
create index if not exists sandbox_usage_org_created on billing.sandbox_usage(org_id, created_at desc);
create index if not exists sandbox_usage_org_sandbox on billing.sandbox_usage(org_id, sandbox_id);

comment on table billing.sandbox_usage is
  'Per-sandbox billing breakdown; charge stays in billing.ledger (reason=sandbox_usage)';
comment on column billing.sandbox_usage.org_id is 'Billing organization (public.organizations.id)';
comment on column billing.sandbox_usage.user_id is 'External product user id, nullable when the product has no user context';
comment on column billing.sandbox_usage.product is 'Product key that owns the sandbox: dslpha or market';
comment on column billing.sandbox_usage.sandbox_id is 'Logical sandbox id, stable across provider restarts';
comment on column billing.sandbox_usage.provider is 'Sandbox provider: daytona or e2b';
comment on column billing.sandbox_usage.provider_sandbox_id is 'Provider-side sandbox id at the time of the run';
comment on column billing.sandbox_usage.run_id is 'Product run/turn id that produced the usage, nullable';
comment on column billing.sandbox_usage.started_at is 'Sandbox run start reported by the product, nullable';
comment on column billing.sandbox_usage.ended_at is 'Sandbox run end reported by the product, nullable';
comment on column billing.sandbox_usage.billable_seconds is 'Billable wall-clock seconds for this row';
comment on column billing.sandbox_usage.cpu_seconds is 'CPU-seconds measured for this run';
comment on column billing.sandbox_usage.memory_mib_seconds is 'Memory MiB-seconds measured for this run';
comment on column billing.sandbox_usage.disk_mib_seconds is 'Disk MiB-seconds measured for this run';
comment on column billing.sandbox_usage.gpu_seconds is 'GPU-seconds measured for this run';
comment on column billing.sandbox_usage.cpu_credits is 'CPU component of the charge in credits';
comment on column billing.sandbox_usage.memory_credits is 'Memory component of the charge in credits';
comment on column billing.sandbox_usage.disk_credits is 'Disk component of the charge in credits';
comment on column billing.sandbox_usage.gpu_credits is 'GPU component of the charge in credits';
comment on column billing.sandbox_usage.total_credits is 'Total charge in whole credits; must be > 0 for a billed row';
comment on column billing.sandbox_usage.credits_minor is 'Charge in console minor units (1000 minor = 1 credit)';
comment on column billing.sandbox_usage.unit_prices is 'Unit prices used for the charge, for display only';
comment on column billing.sandbox_usage.idempotency_key is 'Product-supplied unique key; replay returns the existing row without a second debit';
comment on column billing.sandbox_usage.created_at is 'When the console recorded and charged this report';

alter table billing.sandbox_usage enable row level security;
revoke all on billing.sandbox_usage from anon, authenticated;
revoke all on sequence billing.sandbox_usage_id_seq from anon, authenticated;

-- Application role needs the same access as the other billing tables.
grant select, insert, update, delete on billing.sandbox_usage to roost_api;
grant usage, select on sequence billing.sandbox_usage_id_seq to roost_api;
