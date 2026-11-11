-- Main line: compute market + dslpha + causal-memory. roostbot is paused and
-- no longer receives plan credits or a console entitlement.
update billing.plans set
  product_caps = case code
    when 'free' then '{"dsalpha":500,"market":300,"causal-memory":200}'::jsonb
    when 'starter' then '{"dsalpha":5000,"market":3500,"causal-memory":1500}'::jsonb
    when 'pro' then '{"dsalpha":25000,"market":18000,"causal-memory":7000}'::jsonb
    when 'max' then '{"dsalpha":75000,"market":50000,"causal-memory":25000}'::jsonb
    when 'byok-pro' then '{"dsalpha":10000,"market":7000,"causal-memory":3000}'::jsonb
    else product_caps - 'roostbot' || '{"causal-memory":0}'::jsonb
  end,
  features = jsonb_set(features, '{products}', '["dsalpha","market","causal-memory"]'::jsonb, true)
where code in ('free','starter','pro','max','byok-pro');

delete from billing.entitlements where product = 'roostbot';

insert into billing.entitlements (org_id, product, enabled, monthly_cap_credits, valid_until)
select o.id, 'causal-memory', true,
       coalesce((p.product_caps->>'causal-memory')::bigint, 0),
       (select e.valid_until from billing.entitlements e
          where e.org_id = o.id and e.product = 'dsalpha' limit 1)
from public.organizations o
join billing.plans p on p.code = o.plan_code
on conflict (org_id, product) do update set
  enabled = true,
  monthly_cap_credits = excluded.monthly_cap_credits,
  valid_until = coalesce(excluded.valid_until, billing.entitlements.valid_until),
  updated_at = now();
