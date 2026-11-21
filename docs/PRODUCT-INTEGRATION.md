# 产品接入 Roost Billing

控制面已经部署在：

- 控制台：`https://www.roostbots.com/console/`
- API：`https://www.roostbots.com/console-api`

产品后端只能通过内部接口使用 `SERVICE_SECRET`，不得把该值放进浏览器。

## 1. 身份

所有产品使用同一个 Supabase 项目 `veysvzfcyjxxbhvcfhuv` 登录。

- dslpha：已有 Supabase Auth，补 `org_id` 解析
- roostbot：删除 SQLite account/session，改为验证 Supabase JWT
- market：删除本地 users/sessions，改为验证 Supabase JWT

用户第一次进入控制台时，`/v1/me` 自动创建 personal organization。
`org_id` 是账务和产品权限的唯一维度。

产品必须校验：当前用户确实是 `org_id` 的成员。

## 2. 额度与租约

```http
POST /internal/v1/orgs/resolve-user
Authorization: Bearer $SERVICE_SECRET
{"user_id":"supabase-user-uuid"}

POST /internal/v1/entitlements/resolve
Authorization: Bearer $SERVICE_SECRET
{"org_id":"...","product":"dsalpha"}

POST /internal/v1/usage/leases
{"org_id":"...","product":"dsalpha","run_ref":"run-...","estimated_credits":500,"ttl_seconds":900}

POST /internal/v1/usage/leases/{lease_id}/settle
{"actual_credits":420}

POST /internal/v1/usage/leases/{lease_id}/release
{}

POST /internal/v1/usage/sandbox
{"org_id":"...","product":"market","sandbox_id":"sbx-1","provider":"daytona",
 "provider_sandbox_id":"...","run_id":"run-...","started_at":"...","ended_at":"...",
 "billable_seconds":3600,"cpu_seconds":3600,"memory_mib_seconds":3686400,
 "disk_mib_seconds":921600,"gpu_seconds":0,
 "cpu_credits":1.5,"memory_credits":1.5,"disk_credits":1,
 "unit_prices":{"cpu_credits_per_second":0.0004},
 "idempotency_key":"sbx-run-..."}
→ 201 {id, charged_credits, balance_credits, credits_minor, replayed:false}
→ 200 同一 idempotency_key 重放，返回原记录且不重复扣款
→ 402 余额不足，且不会写入明细
```

沙箱上报规则：

- `sandbox_id` 是逻辑沙箱，跨 provider 重启保持不变
- 四个组件 credits 可省略总额；总额取组件之和向上取整，至少 1 credit
- `credits_minor = total_credits * 1000`（控制台约定 1 credit = 1000 minor，100 credits = ¥1）
- 明细行与钱包扣款、ledger（`reason='sandbox_usage'`）在同一事务提交
- `idempotency_key` 由产品生成且全局唯一，重试必须复用

规则：

- 每个可计费 run 使用唯一 `run_ref`
- 开始前 reserve，结束后 settle；失败/取消可以 release
- 同一个 `run_ref` 重试返回同一个 active lease，不会重复冻结
- 余额不足返回 402 `insufficient_credits`
- 产品月度上限返回 429 `product_cap`
- 产品未开通返回 403 `product_disabled`

## 3. 产品映射

| 产品 | product key | 计量 |
|---|---|---|
| dslpha | `dsalpha` | 模型 token、沙箱秒、工具调用折算 credits |
| causal-memory | `causal-memory` | 记忆读写/推理用量 |
| market | `market` | 沙箱秒、节点资源 |

所有产品只使用一个钱包；产品侧只保留操作配额（例如每回合上限），不再保存人民币余额。

## 4. dslpha 最小改造

- 在服务启动时读取 `ROOST_BILLING_URL` / `ROOST_SERVICE_SECRET`
- 把现有 quota-service 的 admission/lease 调用替换成 Roost 的 resolve/lease
- `user_id` → `org_id` 由 Roost 的会员关系解析（或使用 JWT 的 `active_org_id`）
- 旧 `quota-service` 钱包不再作为余额来源
- 升级按钮指向 `https://www.roostbots.com/console/`

## 5. causal-memory 最小改造（替代暂停的 roostbot）

- 使用 Roost OIDC 或同一 Supabase JWT
- 只保留 causal-memory 自己的用量计量
- `workspaces.tenant_id` 使用 `org_id`
- 预算层保留为操作配额；实际扣费调用 Roost
- 控制台显示 Roost 余额链接
- CLI/桌面接入与记忆 token 生命周期见 [CLI.md](CLI.md)：
  `GET /v1/cli/config`（公开引导配置）、
  `DELETE /v1/orgs/{orgID}/products/causal-memory/tokens/{tokenHash}`（撤销），
  列表返回 `last_used_at`

## 6. market 最小改造

- tenant 映射到 `org_id`；创建 org 时 provision Daytona org
- 用 Supabase JWT 替换本地 session
- 租约开始 reserve，按秒 settle
- 停止写本地钱包；只读 Roost 控制台余额

## 7. 失败策略

- **admission**：Roost 不可达时 fail closed（避免未授权花费）
- **mid-turn renewal**：可按产品策略 fail open，并在恢复后结算
- 所有产品必须设置超时，禁止无限等待
- 回调入账只认控制面，产品不得自行加积分

## 8. dslpha compatibility bridge (implemented)

dslpha can point its existing quota-service client at Roost Console without
changing its lease code:

```bash
AUTH_SERVICE_URL=https://www.roostbots.com/console-api
INTERNAL_SERVICE_TOKEN=<SERVICE_SECRET>
USD_TO_CREDITS_RATE=720
```

Roost exposes the legacy paths under `/api/auth/...`, including
`/api/auth/billing/leases/capability`. On release dslpha reads the run's
last heartbeat (`in_flight_credits`) and sends `spent_credits`; Roost settles
the organization wallet exactly once. The change lives in
`LangAlpha` branch `feat/roost-billing` and its
`docs/roost-billing.md`.

Tested against the deployed control plane:

```text
validate  -> 200, plan and remaining credits
acquire   -> 200, granted=true, ceiling_credits
release   -> 200, lease settled at actual spend
```
