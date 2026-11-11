# Roost Causal Memory gateway

Sits in front of causal-memory's `/mcp` on the causal-memory host.

- validates Roost-issued `cm_...` bearer token against `tokens/cloud.json`
- maps token to Roost org
- charges `CAUSAL_CREDITS_PER_CALL` credits per MCP POST
- proxies streaming requests to the causal-memory server
- `storage-meter.mjs` charges tenant DB bytes per UTC day at
  `CAUSAL_CREDITS_PER_GB_DAY`

The token file is written by `cm-token-sync` from the Roost Console internal
token endpoint.


## Host install

```bash
install -m 700 token-sync.py /opt/mcp-suite/causal-memory/token-sync/sync.py
install -m 600 <service-secret> /etc/causal-token-sync.secret
systemctl enable --now cm-token-sync.timer cm-gateway cm-storage-meter.timer
```

Nginx routes `/memory/` to `127.0.0.1:50062` (the gateway), which proxies to
`127.0.0.1:50061` (causal-memory).


## Per-organization QPS

`CAUSAL_QPS_PER_ORG` (default 5) limits every org to a fixed one-second
window inside the gateway, independent of Nginx's per-IP limit. Exceeding it
returns `429` with `Retry-After: 1` before any credit is charged.
