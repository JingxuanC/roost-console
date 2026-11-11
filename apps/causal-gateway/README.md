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
