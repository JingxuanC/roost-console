export interface Plan {
  code: string;
  name: string;
  price_cents: number;
  duration_days: number;
  included_credits: number;
  product_caps: Record<string, number>;
  features: Record<string, unknown>;
}

export interface ProductState {
  product: string;
  enabled: boolean;
  monthly_cap_credits: number | null;
  used_this_month: number;
}

export interface Wallet {
  balance_credits: number;
  reserved_credits: number;
  available_credits: number;
}

export interface BillingView {
  org: { id: string; name: string; slug: string; plan_code: string; role: string };
  plan: Plan;
  wallet: Wallet;
  products: ProductState[];
  always_on_until?: string | null;
}

export interface MeResponse {
  user: { id: string; email: string };
  org: { id: string; name: string; slug: string; plan_code: string; role: string };
  billing: BillingView;
}

export interface Order {
  id: string;
  org_id: string;
  sku: string;
  amount_cents: number;
  credits: number;
  status: string;
  qr_url?: string;
  pay_url?: string;
  created_at: string;
}

const API = (import.meta.env.VITE_API_URL as string | undefined) ?? '';

async function request<T>(path: string, token: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API}${path}`, {
    ...init,
    signal: init?.signal ?? AbortSignal.timeout(15000),
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  });
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    const message = body?.error?.message ?? body?.detail ?? '请求失败';
    throw new Error(message);
  }
  return body as T;
}

export interface RelayKey {
  token_hash: string;
  org_id: string;
  product: string;
  created_at: string;
  last_used_at?: string | null;
  revoked_at?: string | null;
}

export const api = {
  me: (token: string) => request<MeResponse>('/v1/me', token),
  plans: (token: string) => request<{ plans: Plan[] }>('/v1/billing/plans', token),
  createOrder: (token: string, sku: string) =>
    request<{ order: Order }>('/v1/billing/orders', token, { method: 'POST', body: JSON.stringify({ sku }) }),
  getOrder: (token: string, id: string) => request<{ order: Order }>(`/v1/billing/orders/${id}`, token),
  devPay: (token: string, id: string) => request<{ ok: boolean }>(`/v1/dev/orders/${id}/pay`, token, { method: 'POST' }),
  orders: (token: string) => request<{ orders: Order[] }>('/v1/billing/orders', token),
  relayKeys: (token: string, orgId: string) =>
    request<{ tokens: RelayKey[] }>(`/v1/orgs/${orgId}/products/relay/tokens`, token),
  createRelayKey: (token: string, orgId: string) =>
    request<{ token: string; token_hash: string }>(`/v1/orgs/${orgId}/products/relay/token`, token, { method: 'POST' }),
  revokeRelayKey: (token: string, orgId: string, hash: string) =>
    request<{ ok: boolean }>(`/v1/orgs/${orgId}/products/relay/tokens/${hash}`, token, { method: 'DELETE' }),
  issueCausalToken: (token: string, orgId: string) =>
    request<{ token: string; mcp_url: string; org_id: string }>(`/v1/orgs/${orgId}/products/causal-memory/token`, token, { method: 'POST' }),
  authorizeOAuth: (token: string, body: Record<string, string>) =>
    request<{ redirect_url: string }>('/v1/oauth/authorize', token, { method: 'POST', body: JSON.stringify(body) }),
};
