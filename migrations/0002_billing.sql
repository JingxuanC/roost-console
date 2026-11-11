create schema if not exists billing;

create table if not exists billing.plans (
  code text primary key,
  name text not null,
  price_cents integer not null default 0 check (price_cents >= 0),
  duration_days integer not null default 30 check (duration_days >= 0),
  included_credits bigint not null default 0 check (included_credits >= 0),
  product_caps jsonb not null default '{}'::jsonb,
  features jsonb not null default '{}'::jsonb,
  active boolean not null default true,
  sort_order integer not null default 0,
  created_at timestamptz not null default now()
);

create table if not exists billing.wallets (
  org_id uuid primary key references public.organizations(id) on delete cascade,
  balance_credits bigint not null default 0 check (balance_credits >= 0),
  reserved_credits bigint not null default 0 check (reserved_credits >= 0),
  version bigint not null default 0,
  updated_at timestamptz not null default now(),
  check (balance_credits >= reserved_credits)
);

create table if not exists billing.ledger (
  id bigserial primary key,
  org_id uuid not null references public.organizations(id) on delete cascade,
  product text,
  delta_credits bigint not null,
  reason text not null,
  ref_id text,
  idempotency_key text unique,
  created_at timestamptz not null default now()
);
create index if not exists ledger_org_created on billing.ledger(org_id, created_at desc);

create table if not exists billing.usage_leases (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.organizations(id) on delete cascade,
  product text not null,
  run_ref text not null,
  reserved_credits bigint not null check (reserved_credits >= 0),
  status text not null default 'active' check (status in ('active','settled','released','expired')),
  expires_at timestamptz not null,
  settled_credits bigint,
  created_at timestamptz not null default now(),
  unique (org_id, product, run_ref)
);
create index if not exists usage_leases_active on billing.usage_leases(expires_at) where status = 'active';

create table if not exists billing.entitlements (
  org_id uuid not null references public.organizations(id) on delete cascade,
  product text not null,
  enabled boolean not null default true,
  monthly_cap_credits bigint,
  valid_until timestamptz,
  updated_at timestamptz not null default now(),
  primary key (org_id, product)
);

create table if not exists billing.subscriptions (
  org_id uuid primary key references public.organizations(id) on delete cascade,
  plan_code text not null references billing.plans(code),
  status text not null default 'active' check (status in ('active','past_due','canceled','expired')),
  period_start timestamptz not null default now(),
  period_end timestamptz not null,
  source_order_id uuid,
  updated_at timestamptz not null default now()
);

create table if not exists billing.orders (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.organizations(id) on delete cascade,
  out_trade_no varchar(32) not null unique,
  sku text not null,
  amount_cents integer not null check (amount_cents >= 0),
  credits bigint not null default 0,
  status text not null default 'created'
    check (status in ('created','pending','paid','fulfilled','failed','refunded','expired')),
  provider text not null default 'xunhupay',
  provider_order_id text,
  provider_trade_no text unique,
  qr_url text,
  pay_url text,
  raw jsonb not null default '{}'::jsonb,
  paid_at timestamptz,
  fulfilled_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists orders_org_created on billing.orders(org_id, created_at desc);

create table if not exists billing.payment_events (
  id bigserial primary key,
  provider text not null,
  event_id text not null,
  order_id uuid references billing.orders(id) on delete set null,
  kind text not null default 'notify',
  payload jsonb not null default '{}'::jsonb,
  processed_at timestamptz,
  created_at timestamptz not null default now(),
  unique (provider, event_id)
);

create table if not exists billing.admin_audit (
  id bigserial primary key,
  actor text not null,
  action text not null,
  target text,
  detail jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

alter table billing.plans enable row level security;
alter table billing.wallets enable row level security;
alter table billing.ledger enable row level security;
alter table billing.usage_leases enable row level security;
alter table billing.entitlements enable row level security;
alter table billing.subscriptions enable row level security;
alter table billing.orders enable row level security;
alter table billing.payment_events enable row level security;
alter table billing.admin_audit enable row level security;
revoke all on all tables in schema billing from anon, authenticated;
revoke all on all sequences in schema billing from anon, authenticated;

insert into billing.plans
  (code, name, price_cents, duration_days, included_credits, product_caps, features, sort_order)
values
  ('free','Free',0,0,1000,'{"dsalpha":1000,"roostbot":500,"market":500}',
   '{"products":["dsalpha","roostbot","market"]}',0),
  ('starter','Starter',4900,30,10000,'{"dsalpha":7000,"roostbot":2000,"market":1000}',
   '{"products":["dsalpha","roostbot","market"],"support":"email"}',1),
  ('pro','Pro',19900,30,50000,'{"dsalpha":30000,"roostbot":12000,"market":8000}',
   '{"products":["dsalpha","roostbot","market"],"support":"priority"}',2),
  ('max','Max',49900,30,150000,'{"dsalpha":90000,"roostbot":40000,"market":20000}',
   '{"products":["dsalpha","roostbot","market"],"support":"priority"}',3),
  ('byok-pro','BYOK Pro',19900,30,20000,'{"dsalpha":14000,"roostbot":4000,"market":2000}',
   '{"products":["dsalpha","roostbot","market"],"byok":true}',4)
on conflict (code) do nothing;
