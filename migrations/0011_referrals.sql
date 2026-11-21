-- Referral program: one invite code per organization, one rewarded referral per
-- invitee. A referral reward is booked in billing.ledger with reason
-- 'referral_inviter' / 'referral_invitee' in the same transaction as the
-- referrals row, so a replayed claim can never credit twice. The console API is
-- the only reader/writer; PostgREST roles get no access.
create table if not exists billing.referral_codes (
  code text primary key,
  owner_user_id uuid not null references auth.users(id) on delete cascade,
  owner_org_id uuid not null references public.organizations(id) on delete cascade,
  active boolean not null default true,
  uses integer not null default 0 check (uses >= 0),
  max_uses integer not null default 0 check (max_uses >= 0), -- 0 = unlimited
  created_at timestamptz not null default now()
);

-- Each organization has exactly one invite code. The unique index also makes
-- the "ensure code" write idempotent under concurrent console requests.
create unique index if not exists referral_codes_owner_org on billing.referral_codes(owner_org_id);

create table if not exists billing.referrals (
  id uuid primary key default gen_random_uuid(),
  code text not null references billing.referral_codes(code) on delete cascade,
  inviter_user_id uuid not null references auth.users(id) on delete cascade,
  inviter_org_id uuid not null references public.organizations(id) on delete cascade,
  -- One referral per invitee account, enforced by the unique constraint. A
  -- repeat claim returns this row instead of creating a second reward.
  invitee_user_id uuid not null unique references auth.users(id) on delete cascade,
  invitee_org_id uuid not null references public.organizations(id) on delete cascade,
  status text not null default 'rewarded' check (status in ('rewarded')),
  inviter_credits integer not null default 0 check (inviter_credits >= 0),
  invitee_credits integer not null default 0 check (invitee_credits >= 0),
  created_at timestamptz not null default now(),
  rewarded_at timestamptz
);

-- Lookup by code for reporting and the inviter leaderboard. The invitee unique
-- key above is the idempotent-claim path.
create index if not exists referrals_code on billing.referrals(code);
create index if not exists referrals_inviter on billing.referrals(inviter_org_id, created_at desc);
create index if not exists referrals_invitee_org on billing.referrals(invitee_org_id);

comment on table billing.referral_codes is
  'One shareable invite code per organization; codes are uppercase URL-safe';
comment on table billing.referrals is
  'Rewarded referral, unique per invitee; rewards stay in billing.ledger';
comment on column billing.referral_codes.max_uses is 'Maximum successful claims; 0 means unlimited';
comment on column billing.referral_codes.uses is 'Number of successfully rewarded claims';
comment on column billing.referrals.status is 'Current status; rewarded is the only v1 status';
comment on column billing.referrals.inviter_credits is 'Credits granted to the inviter organization';
comment on column billing.referrals.invitee_credits is 'Credits granted to the invitee organization';

alter table billing.referral_codes enable row level security;
alter table billing.referrals enable row level security;
revoke all on billing.referral_codes from anon, authenticated;
revoke all on billing.referrals from anon, authenticated;

-- Application role needs the same access as the other billing tables.
grant select, insert, update, delete on billing.referral_codes to roost_api;
grant select, insert, update, delete on billing.referrals to roost_api;

-- Admin gifts are booked in the ledger and additionally audited with the
-- operator and note in billing.admin_audit.
grant select, insert on billing.admin_audit to roost_api;
grant usage, select on sequence billing.admin_audit_id_seq to roost_api;
