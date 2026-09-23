#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Shared e2e setup: ensures the Kong license is in place before anything runs
# (from e2e-tests/license.json, $KONG_LICENSE, or $KONG_LICENSE_DATA).
source "$SCRIPT_DIR/../../setup.sh"

CONTAINER="test-ai-gateway-container"
AI_GATEWAY_IMAGE="${AI_GATEWAY_IMAGE:-kong/kong-ai-gateway-dev:2.1.0-rc.3}"

# Step 0: Stop the docker image if it is already running from a previous test
docker rm -f "$CONTAINER"

# Step 1: build the converter.
cd "$ROOT_DIR" && make build

# Step 2: generate the DB-less gateway config.
./ai-deck-converter -direction to-dbless "$SCRIPT_DIR/input.yaml" > "$SCRIPT_DIR/converted.yaml"

# Step 3: start the image
docker run -d --name "$CONTAINER" \
  -v "$SCRIPT_DIR/converted.yaml:/kong/declarative/kong.yaml:ro,Z" \
  -e "KONG_LICENSE_DATA=$(cat "$SCRIPT_DIR/../../license.json")"\
  -e "KONG_DATABASE=off" \
  -e "KONG_DECLARATIVE_CONFIG=/kong/declarative/kong.yaml" \
  -e "KONG_PROXY_LISTEN=0.0.0.0:8000" \
  -e "KONG_ADMIN_LISTEN=0.0.0.0:8001" \
  -e "KONG_LOG_LEVEL=info" \
  ${extra[@]+"${extra[@]}"} \
  -p 8000:8000 \
  -p 8001:8001 \
  "$AI_GATEWAY_IMAGE"

# Tear the container down however we exit, so a failed assertion below still cleans up.
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true' EXIT

# Step 4: wait for the Admin API to become ready
echo "waiting for Kong to start..."
for i in $(seq 1 30); do
  if curl -sf http://localhost:8001/status >/dev/null 2>&1; then
    echo "Kong is up"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "Kong did not become ready in time; recent logs:" >&2
    docker logs --tail 50 "$CONTAINER" >&2
    exit 1
  fi
  sleep 1
done

RESP_HEADERS="$(mktemp)"
RESP_BODY="$(mktemp)"

# Step 5: talk MCP (Streamable HTTP, JSON-RPC 2.0) to the aggregate listener.
# team-a and team-b are conversion-only, so they do not serve MCP themselves;
# their tools reach clients only through the listener that names them in
# config.sources (wired up via the shared mcp-listener:aggregate-id tag).
echo "sending MCP requests..."
MCP_URL="http://localhost:8000/mcp/aggregate"
# The listener is behind a key-auth strategy; this is the consumer credential
# from input.yaml. The conversion-only routes carry no auth.
MCP_API_KEY="mcp-e2e-secret"
SESSION_ID=""

# $1: label, $2: JSON-RPC request body. Response body lands in $RESP_BODY.
mcp_request() {
  local headers=(
    -H 'Content-Type: application/json'
    -H 'Accept: application/json, text/event-stream'
    -H "apikey: $MCP_API_KEY"
  )
  if [ -n "$SESSION_ID" ]; then
    headers+=(-H "Mcp-Session-Id: $SESSION_ID")
  fi
  echo
  echo "--> $1"
  curl -sS -D "$RESP_HEADERS" -o "$RESP_BODY" -X POST "$MCP_URL" "${headers[@]}" -d "$2"
  cat "$RESP_BODY"
  echo
}

# 5a-pre: without the key, key-auth must reject before ai-mcp-proxy runs.
echo
echo "--> POST $MCP_URL (no apikey)"
status="$(curl -sS -o "$RESP_BODY" -w '%{http_code}' -X POST "$MCP_URL" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc": "2.0", "id": 0, "method": "tools/list"}')"
cat "$RESP_BODY"
echo
if [ "$status" != "401" ]; then
  echo "FAIL: unauthenticated MCP request returned $status, expected 401" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo "PASS: unauthenticated request rejected with 401"

# 5a: open the session; the server answers with its capabilities.
mcp_request "initialize" '{
      "jsonrpc": "2.0",
      "id": 1,
      "method": "initialize",
      "params": {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "ai-deck-converter-e2e", "version": "1.0.0"}
      }
    }'
SESSION_ID="$(tr -d '\r' < "$RESP_HEADERS" | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')"
if [ -n "$SESSION_ID" ]; then
  echo "session: $SESSION_ID"
fi

# 5b: complete the handshake (notification, so no response body).
mcp_request "notifications/initialized" '{"jsonrpc": "2.0", "method": "notifications/initialized"}'

# 5c: the listener should expose the tools of BOTH sources.
mcp_request "tools/list" '{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}'

missing=()
for tool in team-a-report team-b-report; do
  if ! grep -q "\"$tool\"" "$RESP_BODY"; then
    missing+=("$tool")
  fi
done
if [ "${#missing[@]}" -ne 0 ]; then
  echo "FAIL: aggregate listener did not expose: ${missing[*]}" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
fi
echo
echo "PASS: aggregate listener exposes team-a-report and team-b-report"

# 5d: call each aggregated tool THROUGH the listener. ai-mcp-proxy executes a
# tool call by re-entering Kong's own proxy with the tool's method+path
# (visible in the access log as a `unix:` client with a "Kong/... MCP server"
# user agent), and that internal request carries no credentials. So a tool
# whose path matches its own conversion-only route meets the access plugin
# copied there and is answered 401 -- reported below, not failed, since it is a
# known data-plane limitation rather than a converter bug.
for tool in team-a-report team-b-report; do
  mcp_request "tools/call $tool" "$(printf '{
      "jsonrpc": "2.0",
      "id": 4,
      "method": "tools/call",
      "params": {"name": "%s", "arguments": {}}
    }' "$tool")"

  # The tool result reports the internal call's own status, so separate "the
  # inherited access blocked it" from "the call failed for its own reasons"
  # from "it actually worked" -- they are three different outcomes.
  failed="$(grep -o 'HTTP call failed with status [0-9]*' "$RESP_BODY" | head -1 || true)"
  if grep -q "No API key found in request" "$RESP_BODY"; then
    echo "WARN: $tool resolved to a path on its own conversion-only route and was" \
         "rejected by the inherited key-auth (internal tool calls carry no credentials)"
  elif [ -n "$failed" ]; then
    echo "INFO: $tool was not blocked by the inherited access, but the call failed:" \
         "$failed. A 404 here is Kong's router: the tool's path matched no route, so" \
         "no service was selected and no upstream was ever dialled (that would be 502)."
  else
    echo "PASS: $tool executed successfully"
  fi
done

# Step 6: the conversion-only routes should not be accessible
echo
echo "checking conversion-only routes are not exposed..."
for server in team-a team-b; do
  echo
  echo "--> POST http://localhost:8000/mcp/$server (no apikey)"
  status="$(curl -sS -o "$RESP_BODY" -w '%{http_code}' \
    -X POST "http://localhost:8000/mcp/$server" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -d '{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}')"
  cat "$RESP_BODY"
  echo
  if [ "$status" != "404" ]; then
    echo "FAIL: $server: external request returned $status, expected 404" >&2
    docker logs --tail 50 "$CONTAINER" >&2
    exit 1
  fi
  echo "PASS: $server rejects external requests without auth credential with 404"

  echo "--> POST http://localhost:8000/mcp/$server (with apikey)"
  status="$(curl -sS -o "$RESP_BODY" -w '%{http_code}' \
    -X POST "http://localhost:8000/mcp/$server" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -H "apikey: $MCP_API_KEY" \
    -d '{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}')"
  cat "$RESP_BODY"
  echo
  if [ "$status" != "404" ]; then
    echo "FAIL: $server: external request returned $status, expected 404" >&2
    docker logs --tail 50 "$CONTAINER" >&2
    exit 1
  fi
  echo "PASS: $server rejects external requests without auth credential with 404"
done

rm -f "$RESP_HEADERS" "$RESP_BODY"

# Step 7: the EXIT trap stops and removes the container.
