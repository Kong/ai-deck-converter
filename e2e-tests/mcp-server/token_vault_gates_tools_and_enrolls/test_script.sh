#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Shared e2e setup: ensures the Kong license is in place before anything runs
# (from e2e-tests/license.json, $KONG_LICENSE, or $KONG_LICENSE_DATA).
source "$SCRIPT_DIR/../../setup.sh"

CONTAINER="test-ai-gateway-container"
REDIS_CONTAINER="test-ai-gateway-token-vault-redis"
NETWORK="test-ai-gateway-tv-net"
STATE_DIR="$SCRIPT_DIR/state"
AI_GATEWAY_IMAGE="${AI_GATEWAY_IMAGE:-kong/kong-ai-gateway-dev:35727837758-070f4458537dcb4fe4d6c7321447cf902d2e07c9}"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  if [ -n "${MOCK_PID:-}" ]; then kill "$MOCK_PID" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

# Step 0: stop anything left over from a previous run.
cleanup || true

# Step 1: build the converter.
cd "$ROOT_DIR" && make build

# Step 2: generate the DB-less gateway config.
./ai-deck-converter -direction to-dbless "$SCRIPT_DIR/input.yaml" > "$SCRIPT_DIR/converted.yaml"

# Step 3: start the mock Token Vault (Kong Identity service) and mock upstream
# MCP server, plus the Redis instance the token_vault block points at.
mkdir -p "$STATE_DIR" && rm -f "$STATE_DIR"/enrolled "$STATE_DIR"/upstream-auth.txt "$STATE_DIR"/identity-requests.log
python3 "$SCRIPT_DIR/mock_services.py" "$STATE_DIR" &
MOCK_PID=$!

docker network create "$NETWORK" >/dev/null
docker run -d --name "$REDIS_CONTAINER" --network "$NETWORK" redis:7-alpine >/dev/null
REDIS_IP="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$REDIS_CONTAINER")"

# Step 4: start the gateway. KONG_IDENTITY_SERVICE points the Token Vault's
# RFC 8693 exchange at the mock identity service. --add-host pins the redis
# container's address (Kong's DNS resolver does not always resolve docker's
# embedded nameserver on user-defined networks).
docker run -d --name "$CONTAINER" --network "$NETWORK" --add-host "mcp-tv-redis:$REDIS_IP" \
  -v "$SCRIPT_DIR/converted.yaml:/kong/declarative/kong.yaml:ro,Z" \
  -e "KONG_LICENSE_DATA=$(cat "$SCRIPT_DIR/../../license.json")" \
  -e "KONG_DATABASE=off" \
  -e "KONG_DECLARATIVE_CONFIG=/kong/declarative/kong.yaml" \
  -e "KONG_PROXY_LISTEN=0.0.0.0:8000" \
  -e "KONG_ADMIN_LISTEN=0.0.0.0:8001" \
  -e "KONG_LOG_LEVEL=info" \
  -e "KONG_IDENTITY_SERVICE=http://host.docker.internal:18080" \
  -p 8000:8000 \
  -p 8001:8001 \
  "$AI_GATEWAY_IMAGE"

# Tear the container down however we exit, so a failed assertion below still cleans up.
trap cleanup EXIT

# Step 5: wait for the Admin API to become ready. Kong parsing the declarative
# config without error is itself an assertion: the converter's auth.token_vault
# lowering (including the flattened redis block) must satisfy the plugin schema.
echo "waiting for Kong to start..."
for i in $(seq 1 30); do
  if curl -sf http://localhost:8001/status >/dev/null 2>&1; then
    echo "Kong is up (declarative config with auth.token_vault accepted)"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "FAIL: Kong did not become ready in time; recent logs:" >&2
    docker logs --tail 80 "$CONTAINER" >&2
    exit 1
  fi
  sleep 1
done

RESP_HEADERS="$(mktemp)"
RESP_BODY="$(mktemp)"

MCP_URL="http://localhost:8000/mcp/vaulted"
SUBJECT_TOKEN="e2e-subject-token"

# $1: label, $2: JSON-RPC request body, $3: (optional) session id.
# Response body lands in $RESP_BODY, headers in $RESP_HEADERS.
mcp_request() {
  local label="$1" body="$2" session="${3:-}"
  local headers=(
    -H 'Content-Type: application/json'
    -H 'Accept: application/json, text/event-stream'
    -H "Authorization: Bearer $SUBJECT_TOKEN"
  )
  if [ -n "$session" ]; then
    headers+=(-H "Mcp-Session-Id: $session")
  fi
  echo
  echo "--> $label"
  curl -sS -D "$RESP_HEADERS" -o "$RESP_BODY" -X POST "$MCP_URL" "${headers[@]}" -d "$body"
  cat "$RESP_BODY"
  echo
}

# 6a: without a bearer token there is no subject to exchange, so the plugin
# cannot resolve the upstream credential and the request must not pass through.
echo
echo "--> POST $MCP_URL (no Authorization header)"
status="$(curl -sS -o "$RESP_BODY" -w '%{http_code}' -X POST "$MCP_URL" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "e2e", "version": "1.0"}}}')"
cat "$RESP_BODY"; echo
if [ "$status" != "500" ]; then
  echo "FAIL: request without a subject token returned $status, expected 500" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo "PASS: no subject token -> upstream auth failure (token_vault is active)"

# 6b: open the session with a subject token. The exchange against the mock
# vault answers x_kong_enrollment_required, so the plugin gates the caller and
# answers locally instead of proxying.
mcp_request "initialize (gated)" '{
      "jsonrpc": "2.0",
      "id": 1,
      "method": "initialize",
      "params": {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "ai-deck-converter-e2e", "version": "1.0.0"}
      }
    }'
if ! grep -q '"result"' "$RESP_BODY"; then
  echo "FAIL: gated initialize did not succeed" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
SESSION_ID="$(tr -d '\r' < "$RESP_HEADERS" | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')"
echo "session: $SESSION_ID"

# 6c: complete the handshake (notification, no response body).
mcp_request "notifications/initialized" '{"jsonrpc": "2.0", "method": "notifications/initialized"}' "$SESSION_ID"

# 6d: tools/list must serve ONLY the virtual enrollment tools -- the real
# upstream tools stay hidden until the caller enrolls.
mcp_request "tools/list (gated)" '{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}' "$SESSION_ID"
for tool in token_vault_authenticate token_vault_check_authentication_status; do
  if ! grep -q "\"$tool\"" "$RESP_BODY"; then
    echo "FAIL: gated tools/list did not offer virtual tool $tool" >&2
    docker logs --tail 50 "$CONTAINER" >&2
    exit 1
  fi
done
if grep -q "flights-search" "$RESP_BODY"; then
  echo "FAIL: gated tools/list leaked the real upstream tools" >&2
  exit 1
fi
echo
echo "PASS: gated caller sees only the virtual enrollment tools"

# 6e: calling authenticate returns the vault's enrollment URL.
mcp_request "tools/call token_vault_authenticate" "$(printf '{
      "jsonrpc": "2.0",
      "id": 3,
      "method": "tools/call",
      "params": {"name": "token_vault_authenticate", "arguments": {}}
    }')" "$SESSION_ID"
if ! grep -q "https://auth.example.com/authorize?state=mock-123" "$RESP_BODY"; then
  echo "FAIL: authenticate did not return the enrollment URL" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo
echo "PASS: authenticate returned the enrollment URL"

# 6f: the user "approves access in their browser" -- the mock vault starts
# answering exchanges with a credential.
touch "$STATE_DIR/enrolled"

# 6g: check_authentication_status re-runs the exchange; on success the plugin
# notifies the session (sleeps ~2s to settle the wake) and un-gates the caller.
mcp_request "tools/call token_vault_check_authentication_status" "$(printf '{
      "jsonrpc": "2.0",
      "id": 4,
      "method": "tools/call",
      "params": {"name": "token_vault_check_authentication_status", "arguments": {}}
    }')" "$SESSION_ID"
if ! grep -q "Authorization complete" "$RESP_BODY"; then
  echo "FAIL: check_authentication_status did not confirm enrollment" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo
echo "PASS: check_authentication_status confirmed enrollment"

# 6h: the exchanged credential must have been shared to Redis (L2), encrypted.
if ! docker exec "$REDIS_CONTAINER" redis-cli --scan --pattern 'ai-mcp-proxy:vault:*' | grep -q .; then
  echo "FAIL: no exchanged credential found in the token_vault Redis cache" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo
echo "PASS: exchanged credential cached in the token_vault Redis (L2)"

# 6i: re-initialize (the gated session was minted locally and is not valid
# upstream) and list tools again: now the REAL upstream tools come through,
# proxied with the exchanged credential.
mcp_request "initialize (enrolled)" '{
      "jsonrpc": "2.0",
      "id": 5,
      "method": "initialize",
      "params": {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "ai-deck-converter-e2e", "version": "1.0.0"}
      }
    }'
UPSTREAM_SESSION="$(tr -d '\r' < "$RESP_HEADERS" | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')"
echo "upstream session: $UPSTREAM_SESSION"

mcp_request "tools/list (enrolled)" '{"jsonrpc": "2.0", "id": 6, "method": "tools/list"}' "$UPSTREAM_SESSION"
if ! grep -q "flights-search" "$RESP_BODY"; then
  echo "FAIL: enrolled tools/list did not expose the real upstream tools" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo
echo "PASS: enrolled caller sees the real upstream tools"

# 6j: calling the real tool proves the proxied request carries the exchanged
# credential, not the caller's subject token.
mcp_request "tools/call flights-search" "$(printf '{
      "jsonrpc": "2.0",
      "id": 7,
      "method": "tools/call",
      "params": {"name": "flights-search", "arguments": {}}
    }')" "$UPSTREAM_SESSION"
if ! grep -q "upstream saw Authorization: Bearer upstream-cred-123" "$RESP_BODY"; then
  echo "FAIL: the upstream did not receive the exchanged credential" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo
echo "PASS: upstream received the exchanged credential"

echo
echo "ALL PASSED"
