# Roost 主线安全审查

日期：2026-11-12（Asia/Shanghai）  
范围：Roost Console、market、dslpha、causal-memory、roostbot（已暂停但仍在运行）、Nginx 与部署配置。  
性质：白盒代码审查 + 生产配置检查 + 依赖漏洞扫描 + 有限的在线探测；**不是**完整渗透测试。

## 1. 使用的审查方法与 skill 来源

没有直接执行第三方 skill 脚本（避免引入未审计的代码）；采用它们的审查清单和威胁建模方法：

- [mbart75/appsec-skills](https://github.com/mbart75/appsec-skills)：威胁建模、verified security review、Supabase RLS。
- [natolitech/security-skill](https://github.com/natolitech/security-skill)：应用安全审查与威胁建模。
- [thirtysix/awesome-agent-skills](https://github.com/thirtysix/awesome-agent-skills)：skill 安全筛选与 vetting 方法。
- [OWASP ASVS](https://owasp.org/www-project-application-security-verification-standard/) 与 OWASP Cheat Sheet 系列作为控制基线。

审查覆盖：

1. 身份与授权：Supabase 登录、OIDC issuer、产品 token、内部服务 token、组织成员关系。
2. 资金链路：market reserve/draw/release、dslpha settle、causal call/storage charge。
3. 输入/输出：SQL 参数化、CORS、redirect_uri、回调验签、代理边界。
4. 部署：Nginx、TLS、安全响应头、secret 权限、暴露端口。
5. 依赖：Go `govulncheck`、`go vet`、secret pattern scan；npm audit 因镜像不支持安全审计未得到结果；Rust `cargo audit` 未执行。

## 2. 结论摘要

控制面没有发现“未认证即可扣款/改余额”的直接漏洞；关键资金接口在未带服务 token 时返回 401。  
但仍有若干高风险设计问题，主要集中在：

- 内部服务认证是**静态共享 Bearer secret**，没有请求签名/防重放/按服务隔离。
- OIDC 只有一个 client，market 与 roostbot 共用 `roostbot` client secret。
- 公共登录、支付、token、MCP 调用缺少统一限流。
- causal-memory token 的签发没有数量上限/撤销 UI，测试 token 和测试账号仍在生产。
- roostbot 入口隐藏了，但服务 API 仍公开可达。
- Go 依赖和标准库存在旧版本漏洞，已在本轮升级并重新部署。

## 3. 发现清单

| ID | 严重度 | 发现 | 状态 |
|---|---|---|---|
| SEC-01 | High | 内部服务使用静态共享 Bearer token | 待修 |
| SEC-02 | High | OIDC 单 client secret 被多产品共用，token endpoint 无限流 | 待修 |
| SEC-03 | High | 登录、支付回调、token 签发、MCP 调用缺少统一限流 | 待修 |
| SEC-04 | Medium | causal token 无每组织上限/撤销界面，sync 暴露全部 token hash | 待修 |
| SEC-05 | Medium | 生产仍有测试账号、测试 causal token 和演示数据 | 待清理 |
| SEC-06 | Medium | roostbot 已隐藏入口但 API 仍公开可达 | 待决策 |
| SEC-07 | Medium | `roost_api` DB 角色带 `BYPASSRLS`，RLS 不是纵深防御 | 待修 |
| SEC-08 | Medium | `/console-sb` 反向代理未显式校验证书 | 待修 |
| SEC-09 | Medium | 支付创建响应未验签；退款/退款中状态未闭环 | 待修 |
| SEC-10 | Medium | causal gateway 在 upstream 返回前扣费，失败调用也会扣 credit | 待修 |
| SEC-11 | Low | OIDC RSA key 每次进程启动重新生成，无轮换/JWKS 生命周期 | 待修 |
| SEC-12 | Low | CORS 仍允许任意 localhost origin | 待修 |
| SEC-13 | Low | 生产二进制仍注册 dev pay 路由（仅 auth 后按环境返回 404） | 待修 |
| SEC-14 | Low | 无 CSP；已补 HSTS/nosniff/frame/referrer/permissions | 部分完成 |
| SEC-15 | Low | storage meter 只对当前 token 对应的 org 计费；402 后无降级策略 | 待修 |
| SEC-16 | Low | token 签发/撤销没有独立审计事件 | 待修 |

## 4. 重点问题详情

### SEC-01 内部服务静态 Bearer token

证据：

- `apps/api/internal/httpapi/server.go`：`withService`、`withCompatService` 使用共享 `SERVICE_SECRET`。
- `apps/causal-gateway/server.mjs`、`apps/causal-gateway/storage-meter.mjs`、`internal/roostbilling/client.go` 都持有同一个 secret。
- dslpha 也通过 `INTERNAL_SERVICE_TOKEN` 使用该 secret。

影响：任一产品/主机或环境变量泄露，即可调用：

```text
/internal/v1/orgs/resolve-user
/internal/v1/usage/charge
/internal/v1/usage/leases*
/internal/v1/products/causal-memory/tokens
```

从而读取或消耗任意 org 的 credits、签发 MCP token。当前没有签名、时间戳、nonce 或请求体哈希，无法防重放。

建议：改成 mTLS 或 HMAC（timestamp + nonce + body digest），按服务拆 secret/scope，支持轮换。

### SEC-02 OIDC 单 client

证据：

- `OIDC_CLIENT_ID=roostbot` 同时给 roostbot 和 market 使用。
- `oidcToken` 没有按 IP/client 的限流。
- `OIDC_CLIENT_SECRET` 在两个产品环境中共享。

影响：任一产品环境泄露 secret，就能换取另一个产品可接受的身份 token；也没有 client 级 scope/redirect 隔离。

建议：建立 OIDC client registry；market/roostbot 不同 client id/secret；token endpoint 加限流；定期轮换。

### SEC-03 限流缺失

- Nginx `roostbots.conf` 没有 `limit_req`。
- causal 网关没有 per-token QPS/并发限制。
- 支付 notify、OIDC token、登录代理、token 签发都直接到后端。

影响：暴力破解、token 猜测、MCP 调用刷量、支付回调洪水、资源耗尽。

建议：Nginx `limit_req_zone` + Go 侧 per-user/per-IP token bucket；causal gateway per-org QPS；支付回调单独限流但仍保证幂等。

### SEC-04 causal token 管理

- `POST /v1/orgs/{org}/products/causal-memory/token` 无数量上限。
- 没有撤销 UI/API 暴露给用户；`revoked_at` 只存在数据库。
- `/internal/v1/products/causal-memory/tokens` 返回所有 org 的 token hash。

建议：每 org 最多 N 个 active token；提供撤销；sync 只返回 active，并做分页/签名；记录签发/撤销审计。

### SEC-05 测试资产

生产仍有：

```text
roost-test@example.com
一个测试 Causal Memory token
测试组织 Roost Demo
```

测试 token 存在于 causal token 文件；测试账号口令已知。正式发布前必须删除/轮换，否则是直接入口。

### SEC-06 roostbot 暴露

`https://roostbots.com/` 已 302 到官网，但：

```text
https://roostbots.com/api/auth/providers
https://roostbots.com/api/auth/oidc/callback
```

仍可达；整个 `/api` 仍由 Node app 提供服务。隐藏入口不等于关闭攻击面。

建议：如果暂停，直接停 `roost-api.service` 或在 Nginx 只保留必要的 OIDC callback/回调路径；否则至少加 IP/认证限制。

### SEC-07 DB 权限

`roost_api` 角色创建时使用了 `BYPASSRLS`。API 本身用参数化 SQL，但 RLS 不是纵深防御；如果 API 出现任意 SQL/逻辑漏洞，影响全库。

建议：拆成 billing/service 两个角色；最小表权限；关键表只允许 RPC/函数写；恢复 RLS 作为第二层。

### SEC-08 代理 TLS

`/console-sb/` 反代到固定 IP，配置了 `proxy_ssl_server_name` 和 Host，但没有显式 `proxy_ssl_verify on` / trusted CA。内部网络被劫持时可能被中间人。

建议：打开证书校验并固定 CA/证书；更长期改为内网服务发现 + mTLS。

### SEC-09 支付

- XunhuPay 创建订单的响应没有验签。
- `status=CD/RD/UD` 目前只 ack，不入 refund 状态。
- 没有退款对账/发票状态闭环。

建议：校验响应 hash；为 refund 建事件/状态；回调加时间窗/重放键；对账任务。

### SEC-10 causal 扣费顺序

`apps/causal-gateway/server.mjs` 在转发 upstream 前调用 charge。若 causal-memory 500/超时，用户仍被扣 1 credit。

建议：先转发，成功响应后扣费；或失败时反向退款/不扣；需要幂等和补偿。

## 5. 已验证的控制

- 未认证访问内部接口返回 401：

```text
/internal/v1/orgs/resolve-user
/internal/v1/usage/charge
/internal/v1/products/causal-memory/tokens
```

- causal `/memory/mcp` 无 token 返回 401；有效 token 返回 200，可完成 MCP initialize / tools/list。
- 生产 `/v1/dev/...` 在未认证时 401；代码中 `APP_ENV=production` 会返回 404。
- 支付回调有金额校验和 `Fulfill` 幂等；创建订单只使用服务端 SKU 价格。
- 组织成员校验：token 签发、billing 查询、dev pay 均走 membership。
- secret 文件权限：

```text
/etc/roost-console/api.env          0600 root
/etc/roost/api.env                  0640 roost:roost
/etc/cm-gateway.env                 0600 root
/etc/causal-token-sync.secret       0600 root
causal cloud.json                   0640 root
```

- 已加安全响应头：

```text
X-Content-Type-Options: nosniff
X-Frame-Options: SAMEORIGIN
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: camera=(), microphone=(), geolocation=()
Strict-Transport-Security: max-age=31536000; includeSubDomains
```

## 6. 依赖扫描结果

初始：

- Console 用 Go 1.24.5 构建，`govulncheck` 报 41 个可达漏洞（含 net/http、crypto/tls、x509、pgx）。
- market 用 Go 1.25.0 构建，报 46 个可达漏洞。

修复后：

- 使用 Go 1.26.11 重建。
- Console：`pgx v5.9.2`、`x/text v0.41.0`，`govulncheck` → **No vulnerabilities found**。
- market：`pgx v5.9.2`、`x/net v0.62.0`、`x/text v0.43.0`，`govulncheck` → **0 reachable vulnerabilities**（仍有 1 个模块级漏洞不在调用路径）。
- `go vet`：两个服务均通过。
- secret pattern scan：未发现真实生产 secret 入库；命中的是测试 fixture/示例。
- npm audit：npm 镜像不支持 audit endpoint，未得到结果。
- cargo audit：本轮未执行。

## 7. 本轮已实施修复

1. Go 工具链升级到 1.26.11，更新 pgx/x/text/x/net，重建并部署 Console 与 market。
2. Nginx（Roost 与 causal-memory）补：
   - HSTS
   - nosniff
   - X-Frame-Options
   - Referrer-Policy
   - Permissions-Policy
   - server_tokens off
3. 验证生产服务健康、内部接口 401、causal token/MCP 鉴权正常。
4. 记录 Causal Memory token 同步与计费服务，便于后续审计。

## 8. 建议优先级

P0（发布前）：

1. SEC-05：删除测试账号、测试 causal token、演示组织。
2. SEC-01：内部服务 mTLS/HMAC + 分服务 secret。
3. SEC-03：Nginx + 应用限流；支付/OIDC/token 单独 bucket。
4. SEC-06：停掉或防火墙 roostbot 公开 API。

P1：

5. SEC-02：拆 OIDC client/secret，做轮换。
6. SEC-04：token 上限、撤销、审计。
7. SEC-08：`proxy_ssl_verify on` + CA 固定。
8. SEC-09：支付响应验签、退款状态、对账。
9. SEC-10：causal 扣费与 upstream 成功解耦。

P2：

10. SEC-07：DB 角色/RLS 纵深。
11. SEC-11/12/13/14/15/16：key 生命周期、CORS、dev 路由移除、CSP、storage 降级、token 审计。

## 9. 限制

- 没有做外部黑盒渗透、DDoS/压测、供应链构建完整性验证。
- 没有测试真实虎皮椒支付（生产 `XUNHUPAY_APPID/APPSECRET` 为空）。
- 没有做 Rust `cargo audit`。
- npm audit 因 registry 镜像不支持而无法得出结论。
- 在线探测是有限 curl 检查，不等于完整 DAST。
