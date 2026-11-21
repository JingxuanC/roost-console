import type { EmailOtpType } from '@supabase/supabase-js';
import { supabase } from './supabase';

/**
 * Supabase email links can carry a `token_hash` instead of the implicit-flow
 * `#access_token` fragment. `detectSessionInUrl` only understands the implicit
 * shape, so the console redeems the hash itself. This is what makes the email
 * template portable across apps: it can point at `{{ .RedirectTo }}` and every
 * app receives a link it can actually finish.
 */
const OTP_TYPES: readonly EmailOtpType[] = [
  'signup',
  'invite',
  'magiclink',
  'recovery',
  'email_change',
  'email',
];

export interface EmailConfirmResult {
  /** Whether this page load carried an email token at all. */
  handled: boolean;
  /** Supabase error message when the redemption failed. */
  error: string | null;
  type: EmailOtpType | null;
}

export async function redeemEmailTokenHash(): Promise<EmailConfirmResult> {
  const url = new URL(window.location.href);
  const tokenHash = url.searchParams.get('token_hash');
  const rawType = url.searchParams.get('type');
  if (!tokenHash || !rawType || !OTP_TYPES.includes(rawType as EmailOtpType)) {
    return { handled: false, error: null, type: null };
  }

  // Drop the single-use token before the request: a refresh, a StrictMode
  // remount or a back-navigation must never replay it.
  url.searchParams.delete('token_hash');
  url.searchParams.delete('type');
  window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`);

  const type = rawType as EmailOtpType;
  const { error } = await supabase.auth.verifyOtp({ token_hash: tokenHash, type });
  return { handled: true, error: error?.message ?? null, type };
}
