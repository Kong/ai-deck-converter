# e2e

End-to-end cases that run the converter and drive a real Kong AI Gateway
container. Everything lives here: the harness (`harness_*.go`, build tag
`e2e`), the case fixtures (`testdata/<case>/`), your license file
(`license.json`, gitignored), and failure artifacts (`artifacts/`,
gitignored).

## Prerequisites

- Docker running locally.
- A Kong Enterprise license, any of the following (checked in this order):
    1. the `KONG_LICENSE` environment variable set to the license JSON, or
    2. the `KONG_LICENSE_DATA` environment variable set to the license JSON, or
    3. a populated `e2e/license.json` (the value of the "Monthly Kong Gateway Enterprise License secret" in 1password).

  Without a license the cases are **skipped**, not failed.
- No build step: the harness converts in-process with the same `convert`
  package the CLI uses.

## Usage

```sh
make e2e                                   # run every case, sequentially
make e2e-case CASE=token_vault_gates_tools_and_enrolls   # run one case
make e2e-update                            # (re)generate expected.yaml snapshots
```

or with go test directly:

```sh
go test -tags=e2e ./e2e -v
go test -tags=e2e ./e2e -v -run 'TestE2E/single_model_multiple_aliases'
```

The gateway image each case uses can be overridden via `AI_GATEWAY_IMAGE`
(overrides all cases; each case also has its own verified default):

```sh
AI_GATEWAY_IMAGE=kong/kong-ai-gateway-dev:2.1.0-rc.4 make e2e
```

Cases run sequentially because each one boots a full gateway; set
`AIDC_E2E_PARALLEL=1` to run them concurrently (needs a Docker VM with room
for three Kong nodes).

## What each case does

Every case:

1. converts its `input.yaml` with the production converter code and compares
   the output byte-for-byte against the checked-in `expected.yaml` snapshot —
   a converter regression fails here with a diff before any gateway is booted;
2. starts a Kong AI Gateway container on free ports with the converted config
   (cases needing a local upstream get a mock server started automatically and
   the input patched to point at it);
3. drives the proxy and asserts the behavior described below.

| Case | What it proves |
| --- | --- |
| `single_model_multiple_aliases` | Each alias of a body-selector model routes through its own `ai-proxy-advanced` copy to the (mocked) upstream, with the provider credential applied. |
| `reusable_toolsets_have_authenticated_routes` | An aggregate MCP listener over conversion-only sources serves both sources' tools; the listener's key-auth is copied onto the conversion-only routes. |
| `token_vault_gates_tools_and_enrolls` | The Token Vault lifecycle: unenrolled callers see only the virtual enrollment tools, enrollment unlocks the real tools, and the exchanged credential is applied upstream and cached in Redis. |

## Failures

On failure the harness writes debugging artifacts under `e2e/artifacts/<case>/`
(gitignored): the converted config that was mounted and the gateway
container's last 500 log lines.
