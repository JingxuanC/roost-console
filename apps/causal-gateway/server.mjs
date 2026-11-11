// Roost Causal Memory gateway: token -> org, per-call charging, stream proxy.
// Runs on the causal-memory host as a systemd service, in front of :50061.
import http from 'node:http';
import fs from 'node:fs';
import crypto from 'node:crypto';

const PORT = Number(process.env.PORT || 50062);
const UPSTREAM_PORT = Number(process.env.CAUSAL_UPSTREAM_PORT || 50061);
const TOKENS_FILE = process.env.CAUSAL_TOKENS_FILE || '/opt/mcp-suite/causal-memory/tokens/cloud.json';
const CONSOLE_URL = (process.env.ROOST_CONSOLE_API_URL || 'https://www.roostbots.com/console-api').replace(/\/+$/, '');
const SERVICE_SECRET = process.env.ROOST_SERVICE_SECRET || '';
const CREDITS_PER_CALL = Number(process.env.CAUSAL_CREDITS_PER_CALL || 1);
const SERVICE_NAME = process.env.ROOST_SERVICE_NAME || 'causal';
const ORG_QPS = Number(process.env.CAUSAL_QPS_PER_ORG || 5);

// Fixed one-second window per organization. The gateway is single-process on
// the causal-memory host; if it is ever scaled out this needs a shared store.
const orgWindows = new Map();
function allowOrg(orgId, now = Date.now()) {
  if (!Number.isFinite(ORG_QPS) || ORG_QPS <= 0) return true;
  const w = orgWindows.get(orgId);
  if (!w || now - w.start >= 1000) {
    orgWindows.set(orgId, { start: now, count: 1 });
    if (orgWindows.size > 10000) {
      for (const [key, value] of orgWindows) {
        if (now - value.start >= 1000) orgWindows.delete(key);
      }
    }
    return true;
  }
  if (w.count >= ORG_QPS) return false;
  w.count += 1;
  return true;
}

function signedHeaders(method, path, body) {
  const ts = String(Math.floor(Date.now() / 1000));
  const nonce = crypto.randomBytes(16).toString('hex');
  const digest = crypto.createHash('sha256').update(body).digest('hex');
  const input = `${SERVICE_NAME}\n${ts}\n${nonce}\n${method}\n${path}\n${digest}`;
  const signature = crypto.createHmac('sha256', SERVICE_SECRET).update(input).digest('hex');
  return {
    'X-Roost-Service': SERVICE_NAME,
    'X-Roost-Timestamp': ts,
    'X-Roost-Nonce': nonce,
    'X-Roost-Signature': signature,
    'content-type': 'application/json',
  };
}

function sha256(text) { return crypto.createHash('sha256').update(text).digest('hex'); }
function tokenMap() {
  try { return JSON.parse(fs.readFileSync(TOKENS_FILE, 'utf8')); } catch { return {}; }
}
function json(res, status, body) {
  const raw = JSON.stringify(body);
  res.writeHead(status, { 'content-type': 'application/json', 'content-length': Buffer.byteLength(raw) });
  res.end(raw);
}
async function charge(orgId, ref, credits) {
  if (!SERVICE_SECRET || credits <= 0) return true;
  try {
    const response = await fetch(CONSOLE_URL + '/internal/v1/usage/charge', {
      method: 'POST',
      headers: signedHeaders('POST', '/internal/v1/usage/charge', JSON.stringify({ org_id: orgId, product: 'causal-memory', ref, credits })),
      body: JSON.stringify({ org_id: orgId, product: 'causal-memory', ref, credits }),
      signal: AbortSignal.timeout(8000),
    });
    if (response.status === 402) return false;
    if (!response.ok) { console.error('charge failed', response.status, await response.text()); return true; }
    return true;
  } catch (err) {
    console.error('charge unreachable; failing open', err.message);
    return true;
  }
}

const server = http.createServer(async (req, res) => {
  if (req.url === '/healthz' || req.url === '/health') return json(res, 200, { ok: true });
  const auth = req.headers.authorization || '';
  const token = auth.startsWith('Bearer ') ? auth.slice(7).trim() : '';
  if (!token) return json(res, 401, { error: 'missing bearer token' });
  const orgId = tokenMap()['sha256:' + sha256(token)];
  if (!orgId) return json(res, 401, { error: 'unknown bearer token' });
  if (!allowOrg(orgId)) {
    res.setHeader('Retry-After', '1');
    return json(res, 429, { error: 'organization rate limit exceeded' });
  }

  const isCall = req.method === 'POST' && (req.url === '/mcp' || req.url.startsWith('/mcp?'));
  if (isCall && CREDITS_PER_CALL > 0) {
    const ok = await charge(orgId, 'cm:call:' + crypto.randomUUID(), CREDITS_PER_CALL);
    if (!ok) return json(res, 402, { error: 'insufficient Roost credits' });
  }

  const upstream = http.request({
    host: '127.0.0.1', port: UPSTREAM_PORT, path: req.url, method: req.method,
    headers: { ...req.headers, host: `127.0.0.1:${UPSTREAM_PORT}` },
  }, (up) => {
    res.writeHead(up.statusCode || 502, up.headers);
    up.pipe(res);
  });
  upstream.on('error', (err) => {
    console.error('upstream error', err.message);
    if (!res.headersSent) json(res, 502, { error: 'causal-memory upstream unavailable' });
    else res.destroy();
  });
  req.pipe(upstream);
});

server.listen(PORT, '127.0.0.1', () => console.log('causal gateway on', PORT));
