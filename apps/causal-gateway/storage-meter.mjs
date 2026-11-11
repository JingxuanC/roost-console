// Daily storage meter: tenant DB bytes -> Roost credits, idempotent per UTC day.
import fs from 'node:fs';
import crypto from 'node:crypto';
import path from 'node:path';

const TENANTS_DIR = process.env.CAUSAL_TENANTS_DIR || '/opt/mcp-suite/causal-memory/cm-data-home/tenants';
const TOKENS_FILE = process.env.CAUSAL_TOKENS_FILE || '/opt/mcp-suite/causal-memory/tokens/cloud.json';
const CONSOLE_URL = (process.env.ROOST_CONSOLE_API_URL || 'https://www.roostbots.com/console-api').replace(/\/+$/, '');
const SERVICE_SECRET = process.env.ROOST_SERVICE_SECRET || '';
const CREDITS_PER_GB_DAY = Number(process.env.CAUSAL_CREDITS_PER_GB_DAY || 10);
const SERVICE_NAME = process.env.ROOST_SERVICE_NAME || 'causal';

function signedHeaders(path, body) {
  const ts = String(Math.floor(Date.now() / 1000));
  const nonce = crypto.randomBytes(16).toString('hex');
  const digest = crypto.createHash('sha256').update(body).digest('hex');
  const input = `${SERVICE_NAME}\n${ts}\n${nonce}\nPOST\n${path}\n${digest}`;
  const signature = crypto.createHmac('sha256', SERVICE_SECRET).update(input).digest('hex');
  return {
    'X-Roost-Service': SERVICE_NAME,
    'X-Roost-Timestamp': ts,
    'X-Roost-Nonce': nonce,
    'X-Roost-Signature': signature,
    'content-type': 'application/json',
  };
}
const day = new Date().toISOString().slice(0, 10);

function readTokens() {
  try { return JSON.parse(fs.readFileSync(TOKENS_FILE, 'utf8')); } catch { return {}; }
}
function orgs() {
  const set = new Set();
  for (const value of Object.values(readTokens())) if (typeof value === 'string' && value) set.add(value);
  return set;
}
async function charge(orgId, credits) {
  const payload = JSON.stringify({ org_id: orgId, product: 'causal-memory', ref: `cm:storage:${orgId}:${day}`, credits });
  const response = await fetch(CONSOLE_URL + '/internal/v1/usage/charge', {
    method: 'POST',
    headers: signedHeaders('/internal/v1/usage/charge', payload),
    body: payload,
    signal: AbortSignal.timeout(10000),
  });
  if (!response.ok) throw new Error(`charge ${response.status}: ${await response.text()}`);
}
let files = [];
try { files = fs.readdirSync(TENANTS_DIR); } catch (err) { console.error(err.message); process.exit(0); }
const sizes = new Map();
for (const name of files) {
  const match = /^([0-9a-f-]{36})\./i.exec(name);
  if (!match) continue;
  const org = match[1].toLowerCase();
  if (!orgs().has(org)) continue;
  const full = path.join(TENANTS_DIR, name);
  try { sizes.set(org, (sizes.get(org) || 0) + fs.statSync(full).size); } catch { /* raced */ }
}
for (const [org, bytes] of sizes) {
  const credits = Math.max(1, Math.ceil((bytes / 1e9) * CREDITS_PER_GB_DAY));
  try { await charge(org, credits); console.log('storage charge', org, bytes, credits); }
  catch (err) { console.error('storage charge failed', org, err.message); }
}
