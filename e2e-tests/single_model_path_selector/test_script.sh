#!/usr/bin/env bash
set -euo pipefail

CONTAINER="test-ai-gateway-container"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Step 0: Stop the docker image if it is already running from a previous test
docker rm -f "$CONTAINER"

# Step 1: build the converter.
cd "$ROOT_DIR" && make build

# Step 2: generate the DB-less gateway config.
./ai-deck-converter -direction to-dbless "$SCRIPT_DIR/input.yaml" > "$SCRIPT_DIR/converted.yaml"

# Step 3: start the image
docker run -d --name "$CONTAINER" \
  -v "$SCRIPT_DIR/converted.yaml:/kong/declarative/kong.yaml:ro,Z" \
  -e "KONG_LICENSE_DATA=$(cat "$SCRIPT_DIR/../license.json")"\
  -e "KONG_DATABASE=off" \
  -e "KONG_DECLARATIVE_CONFIG=/kong/declarative/kong.yaml" \
  -e "KONG_PROXY_LISTEN=0.0.0.0:8000" \
  -e "KONG_ADMIN_LISTEN=0.0.0.0:8001" \
  -e "KONG_LOG_LEVEL=info" \
  ${extra[@]+"${extra[@]}"} \
  -p 8000:8000 \
  -p 8001:8001 \
  kong/kong-ai-gateway-dev:2.0.1-rc.4

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

# Step 5: send a proxy request.
echo "sending proxy request..."
curl -X POST http://localhost:8000/my-model-path-alias-value/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
        "model": "gpt-99999999",
        "messages": [{"role": "user", "content": "Say hello in one word."}]
      }'

# Step 6: Stop the docker image & rm the config file
docker rm -f "$CONTAINER"