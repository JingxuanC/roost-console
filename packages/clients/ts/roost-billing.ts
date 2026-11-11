/**
 * Minimal Roost Console billing client for a product backend.
 * Never put SERVICE_SECRET in a browser bundle.
 */
export interface RoostEntitlement {
  org_id: string;
  product: string;
  enabled: boolean;
  plan_code: string;
  monthly_cap_credits: number | null;
  available_credits: number;
  balance_credits: number;
  reserved_credits: number;
}

export interface RoostLease {
  lease_id: string;
  org_id: string;
  product: string;
  run_ref: string;
  reserved_credits: number;
  status: string;
  expires_at: string;
}

export class RoostBillingClient {
  constructor(
    private readonly baseUrl: string,
    private readonly serviceSecret: string,
    private readonly product: string,
  ) {}

  private async call<T>(path: string, body: unknown): Promise<T> {
    const response = await fetch(`${this.baseUrl.replace(/\/$/, '')}${path}`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.serviceSecret}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(10_000),
    });
    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`roost billing ${response.status}: ${text.slice(0, 200)}`);
    }
    return (await response.json()) as T;
  }

  resolve(orgId: string) {
    return this.call<RoostEntitlement>('/internal/v1/entitlements/resolve', {
      org_id: orgId,
      product: this.product,
    });
  }

  reserve(orgId: string, runRef: string, estimatedCredits: number, ttlSeconds = 900) {
    return this.call<RoostLease>('/internal/v1/usage/leases', {
      org_id: orgId,
      product: this.product,
      run_ref: runRef,
      estimated_credits: estimatedCredits,
      ttl_seconds: ttlSeconds,
    });
  }

  settle(leaseId: string, actualCredits: number) {
    return this.call<RoostLease>(`/internal/v1/usage/leases/${leaseId}/settle`, {
      actual_credits: actualCredits,
    });
  }

  release(leaseId: string) {
    return this.call<{ released: boolean }>(`/internal/v1/usage/leases/${leaseId}/release`, {});
  }
}
