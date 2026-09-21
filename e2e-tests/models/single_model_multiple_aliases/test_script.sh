#!/usr/bin/env bash
set -euo pipefail

CONTAINER="test-ai-gateway-container"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
AI_GATEWAY_IMAGE="${AI_GATEWAY_IMAGE:-kong/kong-ai-gateway:2.0.3}"

RESP_HEADERS="$(mktemp)"
RESP_BODY="$(mktemp)"

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

# The assertions below exit early on failure, so tear the container down and
# drop the scratch files however we leave.
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -f "$RESP_HEADERS" "$RESP_BODY"' EXIT

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

# $1: alias under test, $2: what failed.
fail() {
  echo "FAIL [$1]: $2" >&2
  echo "--- response headers ---" >&2
  cat "$RESP_HEADERS" >&2
  echo "--- response body ---" >&2
  cat "$RESP_BODY" >&2
  echo "--- kong logs ---" >&2
  docker logs --tail 50 "$CONTAINER" >&2
  exit 1
}

# Send one request naming $1 in the my-model-alias body field and assert it was
# proxied out to OpenAI. ai-model-selector reads the alias from that field,
# which picks that alias's own ai-proxy-advanced copy, which must then replace
# the shared service's placeholder upstream (ai-gateway.upstream.local) with
# OpenAI's real host.
assert_alias_proxies_to_openai() {
  local alias="$1"
  local status headers marker

  echo
  echo "--> POST /ai-body-alias/chat/completions  (my-model-alias: $alias)"
  status="$(curl -sS -D "$RESP_HEADERS" -o "$RESP_BODY" -w '%{http_code}' \
    -X POST http://localhost:8000/ai-body-alias/chat/completions \
    -H 'Content-Type: application/json' \
    -d "$(printf '{
          "my-model-alias": "%s",
          "messages": [{"role": "user", "content": "Say hello in one word."}]
        }' "$alias")")"
  cat "$RESP_HEADERS"
  cat "$RESP_BODY"
  echo

  # Normalize header case once so the checks below are case-insensitive.
  headers="$(tr -d '\r' < "$RESP_HEADERS" | tr '[:upper:]' '[:lower:]')"

  # (a) the response came back through Kong at all.
  if ! grep -q '^via:.*kong' <<<"$headers"; then
    fail "$alias" "no 'Via: ... kong' header: the response did not come through the gateway"
  fi
  echo "PASS [$alias]: response carries Kong's Via header"

  # (b) Kong actually dialled an upstream. X-Kong-Upstream-Latency is only set
  # once a real upstream request happened, so it separates a proxied response
  # from one Kong generated itself (router 404, plugin 401, or the 503 you get
  # when the placeholder host is dialled because ai-proxy-advanced never took
  # over -- which is what an unrouted alias would look like).
  if ! grep -q '^x-kong-upstream-latency:' <<<"$headers"; then
    fail "$alias" "no X-Kong-Upstream-Latency header: Kong answered without dialling an upstream.
  Either ai-proxy-advanced did not replace the placeholder host for this alias, or it
  did and the container could not reach OpenAI. Check which name failed:
    docker logs $CONTAINER 2>&1 | grep -iE 'name resolution|dns'"
  fi
  echo "PASS [$alias]: Kong proxied to an upstream ($(grep '^x-kong-upstream-latency:' <<<"$headers"))"

  # (c) that upstream was OpenAI. Any one marker is enough; they are independent
  # so an infrastructure change at OpenAI's edge cannot silently defeat the
  # whole check.
  marker=""
  if grep -q 'api\.openai\.com' <<<"$headers"; then
    marker="api.openai.com in the response headers"
  elif grep -q '^x-request-id: req_' <<<"$headers"; then
    marker="OpenAI-style x-request-id"
  elif grep -qE '^(x-openai|openai-)' <<<"$headers"; then
    marker="openai-prefixed response header"
  fi
  if [ -z "$marker" ]; then
    fail "$alias" "no OpenAI marker in the response headers: Kong proxied somewhere, but not to OpenAI"
  fi
  echo "PASS [$alias]: response originated at OpenAI ($marker)"

  # The status itself is informational. The target's auth header is
  # {vault://ai/openai-token} and input.yaml declares no vaults, so nothing
  # resolves it and OpenAI rejects the call -- expected until a vault and a real
  # key are wired in. What this case asserts is the path, not the credential.
  case "$status" in
    200) echo "PASS [$alias]: OpenAI accepted the request (HTTP 200)" ;;
    401) echo "INFO [$alias]: OpenAI returned 401, expected while {vault://ai/openai-token} is unresolved" ;;
    *)   echo "INFO [$alias]: OpenAI returned HTTP $status" ;;
  esac
}

# Step 5: both aliases of the single model must route. They share one route and
# one ai-model-selector, but the converter emits a separate ai-proxy-advanced
# plugin and ai_models entry per alias (joined by the
# ai-gateway-model-alias-group tag), so each alias is its own path to prove.
echo "sending proxy requests..."
assert_alias_proxies_to_openai my-body-alias-value
assert_alias_proxies_to_openai my-second-alias-value

echo
echo "PASS: both model aliases proxied through the gateway to OpenAI"

# Step 6: the EXIT trap stops the container and removes the scratch files.
