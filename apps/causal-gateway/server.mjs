// Roost Causal Memory gateway: token -> org, per-call charging, stream proxy.
// Runs on the causal-memory host as a systemd service, in front of :50061.
import http from 'node:http';
import fs from 'node:fs';
import crypto from 'node:crypto';
import path from 'node:path';

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

const MAX_BODY_BYTES = Number(process.env.CAUSAL_MAX_BODY_BYTES || 16 * 1024 * 1024);

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on('data', (chunk) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        const err = new Error('request body too large');
        err.statusCode = 413;
        reject(err);
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks)));
    req.on('error', reject);
  });
}

// countToolCalls returns how many JSON-RPC "tools/call" requests the payload
// contains: one for a single object, N for a batch. Unknown / malformed bodies
// are not charged; the upstream will reject them anyway.
function countToolCalls(buffer) {
  if (!buffer || buffer.length === 0) return 0;
  let payload;
  try { payload = JSON.parse(buffer.toString('utf8')); } catch { return 0; }
  const items = Array.isArray(payload) ? payload : [payload];
  let count = 0;
  for (const item of items) {
    if (item && typeof item === 'object' && item.method === 'tools/call') count += 1;
  }
  return count;
}

const AUTH_SKEW_MS = 5 * 60 * 1000;

function verifyServiceSignature(req, path, body) {
  const service = String(req.headers['x-roost-service'] || '').trim();
  const ts = String(req.headers['x-roost-timestamp'] || '').trim();
  const nonce = String(req.headers['x-roost-nonce'] || '').trim();
  const sig = String(req.headers['x-roost-signature'] || '').trim().toLowerCase();
  if (!SERVICE_SECRET || service !== 'causal-sync' || !ts || nonce.length < 8 || !sig) return false;
  const unix = Number(ts);
  if (!Number.isFinite(unix) || Math.abs(Date.now() - unix * 1000) > AUTH_SKEW_MS) return false;
  const digest = crypto.createHash('sha256').update(body).digest('hex');
  const input = `${service}\n${ts}\n${nonce}\nPOST\n${path}\n${digest}`;
  const want = crypto.createHmac('sha256', SERVICE_SECRET).update(input).digest('hex');
  const a = Buffer.from(sig);
  const b = Buffer.from(want);
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}

function writeTokenMapping(tokenHash, orgId) {
  let mapping = {};
  try { mapping = JSON.parse(fs.readFileSync(TOKENS_FILE, 'utf8')); } catch { mapping = {}; }
  mapping['sha256:' + tokenHash] = orgId;
  const dir = path.dirname(TOKENS_FILE);
  fs.mkdirSync(dir, { recursive: true });
  const tmp = path.join(dir, `.cloud.${process.pid}.${Date.now()}.json`);
  fs.writeFileSync(tmp, JSON.stringify(mapping), { mode: 0o640 });
  fs.renameSync(tmp, TOKENS_FILE);
}

const server = http.createServer(async (req, res) => {
  if (req.url === '/healthz' || req.url === '/health') return json(res, 200, { ok: true });
  if (req.method === 'POST' && req.url === '/__token') {
    let body;
    try { body = await readBody(req); } catch (err) { return json(res, err.statusCode || 400, { error: err.message }); }
    if (!verifyServiceSignature(req, '/__token', body)) return json(res, 401, { error: 'bad service signature' });
    let payload;
    try { payload = JSON.parse(body.toString('utf8')); } catch { return json(res, 400, { error: 'invalid json' }); }
    const tokenHash = String(payload.token_hash || '').trim().toLowerCase();
    const orgId = String(payload.org_id || '').trim();
    if (!/^[a-f0-9]{64}$/.test(tokenHash) || !orgId || orgId.length > 128) {
      return json(res, 400, { error: 'invalid token_hash or org_id' });
    }
    try {
      writeTokenMapping(tokenHash, orgId);
    } catch (err) {
      console.error('token push write failed', err.message);
      return json(res, 500, { error: 'token push unavailable' });
    }
    res.writeHead(204);
    return res.end();
  }
  const auth = req.headers.authorization || '';
  const token = auth.startsWith('Bearer ') ? auth.slice(7).trim() : '';
  if (!token) return json(res, 401, { error: 'missing bearer token' });
  const orgId = tokenMap()['sha256:' + sha256(token)];
  if (!orgId) return json(res, 401, { error: 'unknown bearer token' });
  if (!allowOrg(orgId)) {
    res.setHeader('Retry-After', '1');
    return json(res, 429, { error: 'organization rate limit exceeded' });
  }

  const isMCP = req.method === 'POST' && (req.url === '/mcp' || req.url.startsWith('/mcp?'));
  let body = null;
  if (isMCP) {
    try {
      body = await readBody(req);
    } catch (err) {
      return json(res, err.statusCode || 400, { error: err.message });
    }
    // Only MCP tool invocations cost credits. initialize / tools/list /
    // notifications are transport overhead and must stay free, otherwise one
    // conversation would be charged for every protocol round-trip.
    const calls = countToolCalls(body);
    if (calls > 0 && CREDITS_PER_CALL > 0) {
      const ok = await charge(orgId, 'cm:call:' + crypto.randomUUID(), CREDITS_PER_CALL * calls);
      if (!ok) return json(res, 402, { error: 'insufficient Roost credits' });
    }
  }

  const headers = { ...req.headers, host: `127.0.0.1:${UPSTREAM_PORT}` };
  if (body !== null) {
    headers['content-length'] = String(body.length);
    delete headers['transfer-encoding'];
  }
  const upstream = http.request({
    host: '127.0.0.1', port: UPSTREAM_PORT, path: req.url, method: req.method,
    headers,
  }, (up) => {
    res.writeHead(up.statusCode || 502, up.headers);
    up.pipe(res);
  });
  upstream.on('error', (err) => {
    console.error('upstream error', err.message);
    if (!res.headersSent) json(res, 502, { error: 'causal-memory upstream unavailable' });
    else res.destroy();
  });
  if (body !== null) upstream.end(body);
  else req.pipe(upstream);
});

server.listen(PORT, '127.0.0.1', () => console.log('causal gateway on', PORT));
