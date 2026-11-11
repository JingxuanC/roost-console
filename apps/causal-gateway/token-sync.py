#!/usr/bin/env python3
"""Sync Roost-issued Causal Memory tokens into causal-memory's tenant token file."""
import hashlib, hmac, json, os, pathlib, secrets, tempfile, time, urllib.request

BASE = os.environ.get("ROOST_CONSOLE_API_URL", "https://www.roostbots.com/console-api").rstrip("/")
SECRET = pathlib.Path(os.environ.get("ROOST_SERVICE_SECRET_FILE", "/etc/causal-token-sync.secret")).read_text().strip()
OUT = pathlib.Path(os.environ.get("CAUSAL_TOKENS_DIR", "/opt/mcp-suite/causal-memory/tokens")) / "cloud.json"

def post(path, body):
    raw = json.dumps(body, separators=(",", ":")).encode()
    ts = str(int(time.time()))
    nonce = secrets.token_hex(16)
    digest = hashlib.sha256(raw).hexdigest()
    signing_input = "\n".join(["causal-sync", ts, nonce, "POST", path, digest]).encode()
    signature = hmac.new(SECRET.encode(), signing_input, hashlib.sha256).hexdigest()
    req = urllib.request.Request(BASE + path, data=raw, headers={
        "X-Roost-Service": "causal-sync",
        "X-Roost-Timestamp": ts,
        "X-Roost-Nonce": nonce,
        "X-Roost-Signature": signature,
        "Content-Type": "application/json",
    }, method="POST")
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.loads(r.read())

def main():
    data = post("/internal/v1/products/causal-memory/tokens", {})
    mapping = {}
    for item in data.get("tokens", []):
        h = item.get("token_hash", "")
        org = item.get("org_id", "")
        if h and org:
            mapping["sha256:" + h] = org
    OUT.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=OUT.parent, prefix=".cloud.", suffix=".json")
    with os.fdopen(fd, "w") as f:
        json.dump(mapping, f, separators=(",", ":"))
    os.chmod(tmp, 0o640)
    os.replace(tmp, OUT)

if __name__ == "__main__":
    main()
