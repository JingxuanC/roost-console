# Roost Console

统一 Roost 组织、积分、套餐、支付与产品权限的控制面。

状态：Phase 1–3 已上线（2026-11-12）。设计文档见 [docs/SYSTEM-DESIGN.md](docs/SYSTEM-DESIGN.md)。

生产控制台：`https://www.roostbots.com/console/`

## 组成

```text
apps/web      React 19 + Vite + TypeScript 控制台
apps/api      Go API：Supabase JWT、组织、积分账本、用量租约、虎皮椒订单/回调
migrations    Supabase Postgres 迁移
```

## 本地启动

```bash
cd apps/api
cp .env.example .env.local
# 填入 DATABASE_URL、SUPABASE_URL、SUPABASE_ANON_KEY、SERVICE_SECRET
go run ./cmd/roost-console

cd ../web
cp .env.example .env.local
# 填入 VITE_SUPABASE_URL / VITE_SUPABASE_ANON_KEY
npm install
npm run dev
```

控制台默认 `http://127.0.0.1:5175`，API 默认 `http://127.0.0.1:8080`。

## 已实现

- Supabase JWT → 用户、自动 personal org
- `billing.wallets` / `ledger` / `usage_leases` / `entitlements`
- 组织余额、产品月度用量
- 套餐/积分包订单
- 虎皮椒 API 客户端、MD5 签名、回调验签与幂等入账
- 开发模式模拟支付（无商户凭据时）
- dslpha 风格的登录与账单控制台

## 待接

- dslpha compatibility bridge (implemented on LangAlpha `feat/roost-billing`)
- roostbot / market 的 entitlement + usage adapter
- 生产虎皮椒凭据、回调域名
- 邀请成员、发票/退款、到期提醒
- 生产部署（console.roostbots.com / api.roostbots.com）
