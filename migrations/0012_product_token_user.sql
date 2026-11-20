-- Product tokens are issued by an authenticated console user but authorize a
-- machine (relay or causal-memory) for the whole organization. Recording the
-- issuing user binds relay ACP session state to (org, user) instead of only
-- (org), without changing what the token itself authorizes.
--
-- Existing rows keep created_by_user_id = NULL: that is the legacy "org-scoped
-- only" identity, and readers must treat it as "no user binding" rather than
-- guessing an owner. ON DELETE SET NULL keeps a token usable when the issuing
-- user's account is removed from the console; revocation is a separate action.
--
-- This migration is additive. It does not change table-level privileges or RLS
-- policy: the existing product_tokens policies and grants in
-- 0006_product_tokens.sql still apply to the new column.

alter table billing.product_tokens
  add column if not exists created_by_user_id uuid references auth.users(id) on delete set null;

create index if not exists product_tokens_org_product_user
  on billing.product_tokens(org_id, product, created_by_user_id);
