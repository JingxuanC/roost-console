# 内部服务认证

## 先分清两层

- **用户账号层**：一个 Supabase 用户 → 一个 Roost organization → 一个 wallet。
- **机器认证层**：market / dslpha / causal 等后端用自己的 service credential 调 Console。

拆 service secret 不会拆组织、账号或钱包。所有服务最终仍然解析到同一个 `org_id`，扣同一个 wallet。

## 签名

每个内部请求带：

```text
X-Roost-Service: market | dslpha | causal | causal-sync | root
X-Roost-Timestamp: unix seconds
X-Roost-Nonce: 随机串
X-Roost-Signature: HMAC-SHA256
```

签名原文：

```text
service + "\n" +
timestamp + "\n" +
nonce + "\n" +
method + "\n" +
requestURI + "\n" +
sha256(body)
```

Console 侧：

- 时钟窗口 5 分钟
- nonce 单次使用
- 按 `X-Roost-Service` 查对应的 secret 列表
- 再校验 scope
- `root` 只用于运维，不发给产品

## 当前 scope

```text
market
  /internal/v1/orgs/resolve-user
  /internal/v1/entitlements/resolve
  /internal/v1/usage/leases*        reserve / draw / settle / release

dslpha
  /api/auth/validate
  /api/auth/billing/leases*         compat lease

causal
  /internal/v1/usage/charge         call charge

causal-sync
  /internal/v1/products/causal-memory/tokens
```

越权测试：market secret 调 `/internal/v1/usage/charge` 返回 403。

## 配置

Console：

```bash
SERVICE_SECRETS='{"market":["..."],"dslpha":["..."],"causal":["..."],"causal-sync":["..."],"root":["..."]}'
```

每个服务自己的环境变量：

```text
market      ROOST_CONSOLE_SERVICE_SECRET
dslpha      INTERNAL_SERVICE_TOKEN
causal      ROOST_SERVICE_SECRET
sync        /etc/causal-token-sync.secret
```

生成：

```bash
openssl rand -hex 32
```

## 轮换步骤

数组允许一个服务同时有多个 active secret，所以可以不停机轮换：

1. 生成新 secret。
2. 在 Console `SERVICE_SECRETS` 对应服务的数组末尾追加新 secret。
3. 重启 Console，确认旧/新 secret 都能工作。
4. 更新该服务的环境变量并重启该服务。
5. 从 Console 数组移除旧 secret。
6. 重启 Console，确认旧 secret 返回 401。

## 原则

- 不把 service secret 写进 repo、日志、前端。
- 一个产品只拿自己的 secret。
- 一个 org / wallet 不变；变化只在机器凭证的 blast radius。
