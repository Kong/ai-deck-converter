# e2e-tests

## Prerequisites
- Docker running locally.
- A Go toolchain + `make` (for `make build`).
- `curl`.

## Usage

1. Create the Kong license json file in `e2e-tests/license.json`
   1. touch `e2e-tests/license.json`
2. Set the `license.json` value to the value stored in the "Monthly Kong Gateway Enterprise License" secret in 1password


2. Run a test case:
   ```sh
   bash models/single_model_multiple_aliases/test_script.sh
   bash mcp-server/reusable_toolsets_have_authenticated_routes/test_script.sh
   ```

   Each script's `kong/kong-ai-gateway-dev` image tag can be overridden via the
   `AI_GATEWAY_IMAGE` env var, e.g.:
   ```sh
   AI_GATEWAY_IMAGE=kong/kong-ai-gateway-dev:2.1.0-rc.4 bash mcp-server/test_script.sh
   ```
   If unset, each script falls back to the version it was last verified against.

## Notes
