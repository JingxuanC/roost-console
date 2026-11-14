# CLI 引导配置与产品 token 撤销

roost-cli v0.2 做「email/password → Supabase → 无头 OIDC → market token」时，
需要 console API 暴露两类能力：

1. 公开的 CLI 引导配置（不需要登录即可拿到 Supabase 项目与 OIDC 端点）；
2. 已签发 causal-memory token 的列表 / 撤销。

两个端点都在 console API（生产基址 `https://www.roostbots.com/console-api`，
下文简写 `$CONSOLE_API`）。本文件只写服务端契约；CLI 侧命令见 roost-cli 仓库。

## 1. `GET /v1/cli/config`（公开）

CLI 首次启动时读取。**无需 Authorization**，返回的都是可公开值（Supabase anon
key 本身就是浏览器/客户端可见的公开 key），不含 `SERVICE_SECRET`、数据库
URL、Supabase service key、支付或 OIDC client secret。

```bash
curl -sS https://www.roostbots.com/console-api/v1/cli/config
```

```json
{
  "supabase_url": "https://<project>.supabase.co",
  "supabase_anon_key": "<public anon key>",
  "console_api_url": "https://www.roostbots.com/console-api",
  "market_api_url": "https://www.roostbots.com/api",
  "memory_mcp_url": "https://causal-memory.com/memory/mcp",
  "oidc": {
    "issuer": "https://www.roostbots.com/console-api",
    "authorization_endpoint": "https://www.roostbots.com/console-api/oauth/authorize",
    "token_endpoint": "https://www.roostbots.com/console-api/oauth/token"
  }
}
```

| 字段 | 含义 | 服务端来源 / 默认值 |
|---|---|---|
| `supabase_url` | Supabase 项目 URL | `SUPABASE_URL` |
| `supabase_anon_key` | Supabase 匿名 key（公开） | `SUPABASE_ANON_KEY` |
| `console_api_url` | console API 基址 | `CONSOLE_API_URL`，默认 `https://www.roostbots.com/console-api` |
| `market_api_url` | market API 基址 | `MARKET_API_URL`，默认 `https://www.roostbots.com/api` |
| `memory_mcp_url` | causal-memory MCP 端点 | `CAUSAL_MEMORY_MCP_URL`（兼容 `MEMORY_MCP_URL`），默认 `https://causal-memory.com/memory/mcp` |
| `oidc.issuer` | OIDC issuer | `OIDC_ISSUER`，默认 `https://www.roostbots.com/console-api` |
| `oidc.authorization_endpoint` | 授权端点 | `{issuer}/oauth/authorize` |
| `oidc.token_endpoint` | 换票端点 | `{issuer}/oauth/token` |

响应头带 `Cache-Control: no-store`。CORS 走全局中间件：`CONSOLE_ORIGIN`
（以及本地 `http://127.0.0.1:*` / `http://localhost:*`）的浏览器请求允许
`GET`，预检 `OPTIONS` 返回 204；CLI 是原生 HTTP 客户端，不受 CORS 限制。

### 用这些字段完成引导

```text
1. Supabase 密码登录：
   POST {supabase_url}/auth/v1/token?grant_type=password
   apikey: {supabase_anon_key}
   {"email": "...", "password": "..."}
   → access_token（Supabase 用户 JWT）

2. 向 market 取这次 OIDC 授权的参数：
   GET {market_api_url}/v1/auth/oidc/config
   → {"enabled":true,"client_id":"<OIDC client>","redirect_url":"<market callback>",
      "state":"...","nonce":"...","code_challenge":"...","scopes":["openid",...]}

   state/nonce/PKCE verifier 由 market 服务端保存；CLI 不生成也不持有 verifier。

3. 让 console 为当前 Supabase 用户批准这次授权：
   POST {console_api_url}/v1/oauth/authorize
   Authorization: Bearer <Supabase access_token>
   {"response_type":"code",
    "client_id":"<market 返回的 client_id>",
    "redirect_uri":"<market 返回的 redirect_url>",
    "code_challenge":"<market 返回的 code_challenge>",
    "scope":"openid profile email",
    "state":"<market 返回的 state>",
    "nonce":"<market 返回的 nonce>"}
   → {"redirect_url":"<market callback>?code=...&state=..."}

4. CLI 直接 GET 这个 market callback：
   GET <redirect_url>
   → market 服务端用保存的 code_verifier 向 console 的 oidc.token_endpoint
     换 OIDC token，再把 market 自己的 {"access_token","refresh_token",
     "expires_in",...} 返回给 CLI。
```

`redirect_uri` 必须是 market 的 OIDC callback（例如
`https://www.roostbots.com/api/v1/auth/oidc/callback`），并在 console 的
`OIDC_REDIRECT_URIS` 里预先登记；`roost-cli://` 回调不属于当前无头流程。
CLI 不直接调用 `oidc.token_endpoint`，也不持有 `client_secret`：PKCE
verifier 和 client secret 都只在 market 服务端使用。

## 2. `DELETE /v1/orgs/{orgID}/products/causal-memory/tokens/{tokenHash}`

撤销本组织的一个 causal-memory 产品 token。鉴权是 Supabase 用户 JWT
（`Authorization: Bearer <Supabase access_token>`），且调用者必须是 `{orgID}`
的成员。

```bash
curl -sS -X DELETE \
  "https://www.roostbots.com/console-api/v1/orgs/$ORG_ID/products/causal-memory/tokens/$TOKEN_HASH" \
  -H "Authorization: Bearer $SUPABASE_ACCESS_TOKEN"
# 204 No Content
```

| 场景 | HTTP | 说明 |
|---|---|---|
| 撤销成功 | 204 | 无响应体 |
| 重复撤销同一 token | 204 | 幂等；已撤销视为成功 |
| token hash 不属于该 org / 不存在 | 404 `not_found` | 不泄露其他 org 的 token 是否存在 |
| 不是该 org 成员 | 403 `forbidden` | |
| 缺少 / 无效 Supabase JWT | 401 `unauthorized` | |
| 产品不是 `causal-memory` | 400 `unsupported_product` | 目前只支持记忆 token |
| 其他错误 | 500 `internal_error` | |

错误统一信封：

```json
{"error": {"code": "not_found", "message": "token not found"}}
```

### 拿到 `tokenHash`

列表端点返回已签发且未撤销的 token（**不返回原始 `cm_` token**，原始值只在
签发响应当中出现一次）：

```bash
curl -sS "$CONSOLE_API/v1/orgs/$ORG_ID/products/causal-memory/tokens" \
  -H "Authorization: Bearer $SUPABASE_ACCESS_TOKEN"
```

```json
{
  "tokens": [
    {
      "token_hash": "1f0c...e9",
      "org_id": "…",
      "product": "causal-memory",
      "created_at": "2026-11-12T08:00:00Z",
      "last_used_at": "2026-11-12T08:30:00Z"
    }
  ],
  "mcp_url": "https://causal-memory.com/memory/mcp"
}
```

- `last_used_at` 来自 `billing.product_tokens.last_used_at`；从未使用时该字段
  省略（`omitempty`）。当前只读展示，写入由记忆网关的同步任务负责。
- 签发端点仍是 `POST /v1/orgs/{orgID}/products/causal-memory/token`
  （返回 `token` 明文一次 + `token_hash` + `mcp_url`）。

## 3. 服务端环境变量

| 变量 | 默认值 | 用途 |
|---|---|---|
| `CONSOLE_API_URL` | `https://www.roostbots.com/console-api` | CLI 引导配置里的 console API 基址 |
| `MARKET_API_URL` | `https://www.roostbots.com/api` | CLI 引导配置里的 market API 基址 |
| `CAUSAL_MEMORY_MCP_URL` | `https://causal-memory.com/memory/mcp` | 记忆 MCP 端点（也用于 token 签发/列表响应的 `mcp_url`） |
| `MEMORY_MCP_URL` | — | `CAUSAL_MEMORY_MCP_URL` 的兼容别名 |

这些值都是公开配置，可以出现在部署 env 与文档里；不要与
`SERVICE_SECRET(S)`、`SUPABASE_SERVICE_KEY`、`XUNHUPAY_APPSECRET`、
`OIDC_CLIENT_SECRET` 混放。
