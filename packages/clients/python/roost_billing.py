"""Minimal Roost Console billing client for a Python product backend.

Only a product backend may hold SERVICE_SECRET. Use a short timeout: billing
outage handling belongs to the product (fail closed on admission, fail open on
mid-turn renewal is a product policy).
"""
from __future__ import annotations

import httpx


class RoostBillingClient:
    def __init__(self, base_url: str, service_secret: str, product: str, timeout: float = 10.0):
        self._base_url = base_url.rstrip("/")
        self._headers = {
            "Authorization": f"Bearer {service_secret}",
            "Content-Type": "application/json",
        }
        self._product = product
        self._timeout = timeout

    async def _post(self, path: str, body: dict) -> dict:
        async with httpx.AsyncClient(timeout=self._timeout) as client:
            response = await client.post(self._base_url + path, json=body, headers=self._headers)
        if response.status_code >= 400:
            raise RuntimeError(f"roost billing {response.status_code}: {response.text[:200]}")
        return response.json()

    async def resolve(self, org_id: str) -> dict:
        return await self._post(
            "/internal/v1/entitlements/resolve",
            {"org_id": org_id, "product": self._product},
        )

    async def reserve(self, org_id: str, run_ref: str, estimated_credits: int, ttl_seconds: int = 900) -> dict:
        return await self._post(
            "/internal/v1/usage/leases",
            {
                "org_id": org_id,
                "product": self._product,
                "run_ref": run_ref,
                "estimated_credits": estimated_credits,
                "ttl_seconds": ttl_seconds,
            },
        )

    async def settle(self, lease_id: str, actual_credits: int) -> dict:
        return await self._post(
            f"/internal/v1/usage/leases/{lease_id}/settle",
            {"actual_credits": actual_credits},
        )

    async def release(self, lease_id: str) -> dict:
        return await self._post(f"/internal/v1/usage/leases/{lease_id}/release", {})
