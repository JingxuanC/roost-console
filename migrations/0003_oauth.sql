-- Minimal OIDC authorization-code + PKCE surface for first-party products.
-- Access tokens are opaque server-side tokens (not browser-readable JWTs);
-- userinfo is the only consumer.
create table if not exists billing.oauth_codes (
  code_hash text primary key,
  client_id text not null,
  user_id uuid not null,
  email text not null,
  org_id uuid not null references public.organizations(id) on delete cascade,
  redirect_uri text not null,
  code_challenge text not null,
  expires_at timestamptz not null,
  used_at timestamptz,
  created_at timestamptz not null default now()
);
create index if not exists oauth_codes_expiry on billing.oauth_codes(expires_at);

create table if not exists billing.oauth_tokens (
  token_hash text primary key,
  client_id text not null,
  user_id uuid not null,
  email text not null,
  org_id uuid not null references public.organizations(id) on delete cascade,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);
create index if not exists oauth_tokens_expiry on billing.oauth_tokens(expires_at);

alter table billing.oauth_codes enable row level security;
alter table billing.oauth_tokens enable row level security;
revoke all on billing.oauth_codes, billing.oauth_tokens from anon, authenticated;
