create table if not exists billing.product_tokens (
  token_hash text primary key,
  org_id uuid not null references public.organizations(id) on delete cascade,
  product text not null,
  created_at timestamptz not null default now(),
  last_used_at timestamptz,
  revoked_at timestamptz
);
create index if not exists product_tokens_org on billing.product_tokens(org_id, product);
alter table billing.product_tokens enable row level security;
revoke all on billing.product_tokens from anon, authenticated;
