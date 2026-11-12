# Deployment

生产部署当前使用：

```text
www.roostbots.com/console/      静态控制台
www.roostbots.com/console-api/  Go API（反代到 127.0.0.1:8790）
/etc/roost-console/api.env      API 环境变量
roost-console-api.service       systemd
```

部署步骤：

1. `cd apps/api && GOOS=linux GOARCH=amd64 go build -o /tmp/roost-console-api ./cmd/roost-console`
2. `cd apps/web && VITE_SUPABASE_URL=... VITE_SUPABASE_ANON_KEY=... VITE_API_URL=/console-api npm run build -- --base=/console/`
3. 上传 API 二进制和 `apps/web/dist/`。
4. 更新 `/etc/roost-console/api.env`，`systemctl restart roost-console-api`。
5. `nginx -t && systemctl reload nginx`。

不要提交 `api.env`、`SERVICE_SECRET`、Supabase service key 或虎皮椒 appsecret。

## 上线前必须补

- 虎皮椒 `XUNHUPAY_APPID` / `XUNHUPAY_APPSECRET`
- `XUNHUPAY_NOTIFY_URL=https://www.roostbots.com/console-api/v1/payments/xunhupay/notify`
- 生产数据库角色 `roost_api` 和密码
- DNS：`console.roostbots.com` / `api.roostbots.com`（如需独立域名）

可选公开基址（CLI 引导配置 `GET /v1/cli/config` 会返回，默认值已指向现网；
staging 时按需覆盖，均为公开值不是 secret）：

- `CONSOLE_API_URL=https://www.roostbots.com/console-api`
- `MARKET_API_URL=https://www.roostbots.com/api`
- `CAUSAL_MEMORY_MCP_URL=https://causal-memory.com/memory/mcp`

当前使用路径式部署，不依赖新 DNS。
