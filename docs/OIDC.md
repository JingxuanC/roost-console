# Roost OIDC issuer

Roost Console exposes a minimal OIDC authorization-code + PKCE provider so a
first-party product can keep its own session model while using the Roost
organization identity. roostbot already supports generic OIDC providers
(`ROOST_OIDC_<PROVIDER>_*`) and is the first consumer.

## Endpoints

```text
GET  /.well-known/openid-configuration
GET  /oauth/authorize              → browser consent page
POST /oauth/token                  → authorization_code + PKCE S256
GET  /oauth/userinfo               → sub / email / email_verified / org_id
POST /v1/oauth/authorize           → authenticated consent approval, returns redirect_url
```

The browser opens `/console/oauth/authorize?...`; the console uses the existing
Supabase session, shows the consent card, and redirects back with `code` and
`state`. No password is sent to the product.

## Server env

```bash
OIDC_ISSUER=https://www.roostbots.com/console-api
OIDC_CLIENT_ID=roostbot
OIDC_CLIENT_SECRET=<random 32-byte secret>
OIDC_REDIRECT_URIS=https://roostbots.com/api/auth/oidc/callback,https://www.roostbots.com/api/auth/oidc/callback
OIDC_CONSENT_URL=https://www.roostbots.com/console/oauth/authorize
```

The client secret is only used at the token endpoint, server-to-server.

## roostbot env

```bash
ROOST_OIDC_ROOST_CLIENT_ID=roostbot
ROOST_OIDC_ROOST_SECRET=<same OIDC_CLIENT_SECRET>
ROOST_OIDC_ROOST_AUTHORIZE_URL=https://www.roostbots.com/console-api/oauth/authorize
ROOST_OIDC_ROOST_TOKEN_URL=https://www.roostbots.com/console-api/oauth/token
ROOST_OIDC_ROOST_USERINFO_URL=https://www.roostbots.com/console-api/oauth/userinfo
ROOST_OIDC_ROOST_SCOPE=openid email profile
ROOST_OIDC_REDIRECT_URI=https://roostbots.com/api/auth/oidc/callback
```

Then the roostbot login screen can list the `roost` provider. Its
`upsertOidcAccount` creates one local account per Supabase `sub`; the account
still needs the workspace → org mapping before product billing can use the
shared wallet.

## Verified

Local flow:

```text
POST /v1/oauth/authorize  → code + state
POST /oauth/token         → access_token (opaque, 1h)
GET  /oauth/userinfo      → sub/email/org_id
```

The token endpoint verifies client secret, redirect URI and PKCE; authorization
codes are single-use and expire after five minutes.
