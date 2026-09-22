# e2e-tests

## Prerequisites
- Docker running locally.
- A Go toolchain + `make` (for `make build`).
- `curl`.

## Usage

1. Provide the Kong Enterprise license JSON, any of the following ways (checked
   in this order by `setup.sh`, which every test script sources first):
   1. a populated `e2e-tests/license.json` (the value of the "Monthly Kong
      Gateway Enterprise License" secret in 1password), or
   2. the `KONG_LICENSE` environment variable set to the license JSON, or
   3. the `KONG_LICENSE_DATA` environment variable set to the license JSON.

   If `license.json` is missing but an env var is set, `setup.sh` writes
   `license.json` from it; if no license is found anywhere, the test aborts
   with an error.

2. Run a test case:
   ```sh
   bash models/single_model_multiple_aliases/test_script.sh
   bash mcp-server/reusable_toolsets_have_authenticated_routes/test_script.sh
   bash mcp-server/token_vault_gates_tools_and_enrolls/test_script.sh
   ```

   Each script's `kong/kong-ai-gateway-dev` image tag can be overridden via the
   `AI_GATEWAY_IMAGE` env var, e.g.:
   ```sh
   AI_GATEWAY_IMAGE=kong/kong-ai-gateway-dev:2.1.0-rc.4 bash mcp-server/test_script.sh
   ```
   If unset, each script falls back to the version it was last verified against.

## Notes
