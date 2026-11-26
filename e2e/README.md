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

An image tag can start with a release version, for example `2.3.0` or
`2.1.0-rc.4`. Then the test skips each case with a newer min runtime
version after its snapshot check. That data plane rejects the converted config
and never becomes ready. A tag without a version, for example `aigw-master`
or `latest`, runs every case.

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

| Case                                         | What it proves                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
|----------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `single_model_multiple_aliases`              | Each alias of a body-selector model routes through its own `ai-proxy-advanced` copy to the (mocked) upstream, with the provider credential applied.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `pre_function_model_ordering`                | A model policy that outranks `ai-model-selector` (`pre-function`) runs after model selection: the probe sees `ngx.ctx.ai_model` for each guarded alias. A model without the policy on the same route still reaches the (mocked) upstream.                                                                                                                                                                                                                                                                                                                                                                                               |
| `pre_function_model_ordering_auth`           | The same reordering keeps authentication first: on a route with a key-auth auth strategy, a request with no key or a wrong key gets the 401 and the model-scoped `pre-function` probe never runs. A request with a valid key reaches the probe, which sees `ngx.ctx.ai_model`. One auth strategy needs no `anonymous` fallback, so key-auth returns the 401 itself.                                                                                                                                                                                                                                                                     |
| `model_scoped_request_termination_auth`      | On a reordered route with one key-auth auth strategy, key-auth rejects a missing or wrong key with 401 before any model-scoped policy runs. For an authenticated request, a model-scoped `request-termination` blocks its model with 403, the model-scoped `pre-function` probe still sees `ngx.ctx.ai_model`, and a model without policies on the same route reaches the (mocked) upstream.                                                                                                                                                                                                                                            |
| `model_response_transformer_ordering`     | Two models on one route carry a model-scoped `response-transformer-advanced` and `response-transformer` policy. The gateway loads both cloned transformers below `ai-proxy-advanced` (priority 766), so it no longer overwrites the body it normalized, and each transformer changes both the header and the body for a client that sends `Accept-Encoding: identity`. With gzip the advanced transformer still changes both, while the plain one changes only the header; the case pins that documented limit. |
| `gemini_model_all_capabilities`              | A gemini model declaring every capability (generate, embeddings, image, video, rerank, batches, files) is accepted by the data plane, with every Gemini Standard API and Gemini Enterprise AI route loaded.                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `bedrock_model_all_endpoints`                | Every bedrock endpoint (Converse, InvokeModel for chat, embeddings, image, and video, the OpenAI- and Anthropic-compatible chat endpoints, RetrieveAndGenerate, rerank, and batches) and every bedrock mantle endpoint (`openai` chat, responses, and files, `anthropic` messages) is accepted by the data plane, with every route loaded.                                                                                                                                                                                                                                                                                              |
| `bedrock_mantle_vpc_endpoint`                | The 2.3 data plane accepts a bedrock target that sets both `endpoint_type: mantle` and `vpc_endpoint`, on every mantle endpoint (`openai` chat, responses, and files, `anthropic` messages). The loaded `ai-proxy-advanced` targets keep both fields.                                                                                                                                                                                                                                                                                                                                                                                   |
| `reusable_toolsets_are_internal_only`        | An aggregate MCP listener over conversion-only sources serves and executes both sources' tools; the conversion-only and upstream-server routes are gated so direct client requests get 404, with or without credentials.                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `mcp_server_acl_gates_all_methods`           | On a 2.1 data plane, MCP server `access.acls` `allow` and `deny` become a Kong `acl` plugin in front of `ai-mcp-proxy`. A consumer outside the allow list or on the deny list gets 403 from the acl plugin for every MCP operation (initialize, notifications, ping, tools/list, tools/call, resources/list, GET stream, DELETE), and the upstream sees no request. Membership in a tool ACL group does not open the server. A consumer the server ACL lets in still gets the tool ACLs: a tool limited to another group is hidden and blocked. A deny on one server does not affect another.                                           |
| `mcp_server_acl_when`                        | On a 2.3 data plane, MCP server `access.acls` `allow_when` and `deny_when` become CEL conditions on the Kong `acl` plugin. A consumer that does not match `allow_when` (on `consumer_group.names`), or that matches `deny_when` (on `consumer.username`), gets 403 for every MCP operation. Matching consumers reach the upstream.                                                                                                                                                                                                                                                                                                      |
| `mcp_oidc_server_acls`                       | The `openid-connect` lowering of MCP access: the plugin serves the protected resource metadata and rejects a missing or invalid bearer token with 401. Its `authenticated_groups_claim` feeds the server `acl` plugin, so a token without the allowed group gets 403 for every MCP operation. It also publishes the token claims that an `oauth_access_token` tool ACL reads: a token with the allowed `email` sees and calls the tool, any other token does not. A conversion-listener behind the same plugin runs its tools with no `forward_client_headers`: the bearer token survives the internal pass back through the route. |
| `mcp_oidc_consumer_acl` | On a 2.0 data plane, a request whose token maps to a consumer gets through the server `access.acls` allow list if the token has an allowed group or the consumer is in an allowed consumer group; otherwise it gets 403. |
| `realtime_model_websocket_transport`         | A model with chat and realtime capabilities serves both transports: the chat route stays on HTTP, and `ws`/`wss` clients reach the (mocked) upstream through the `ai-gateway-websocket` Service with the provider credential applied. On the WebSocket route, key-auth rejects a missing key with 401 and the ACL rejects a consumer outside the group with 403. The prompt guard, which cannot run on WebSocket, still applies on HTTP.                                                                                                                                                                                                |
| `token_vault_gates_tools_and_enrolls`        | The Token Vault lifecycle: unenrolled callers see only the virtual enrollment tools, enrollment unlocks the real tools, and the exchanged credential is applied upstream and cached in Redis.                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `gemini_interactions_api`                    | The data plane accepts a gemini target on `llm/v1/responses` in the gemini format (the Gemini Interactions API). Fails until a data plane release ships the Interactions API: current images reject it with `gemini does not support llm/v1/responses API`.                                                                                                                                                                                                                                                                                                                                                                             |
| `wildcard_model_all_endpoints_2_0` to `_2_4` | The data plane of each release from 2.0 to 2.4 loads every route of a `capabilities: ["*"]`, `formats: [{type: "*"}]` model with one target of each provider type it supports, and of a `type: api` wildcard model. The 2.3 and 2.4 cases also have a mantle `bedrock` target. The 2.4 case also has a togetherai target.                                                                                                                                                                                                                                                                                                                                                                    |
| `session_affinity_sticks_to_target`          | On a 2.4 data plane, a model with `balancer.session_affinity` and `storage.type: local` becomes the ai-proxy-advanced `conversation_state` block. The second turn of one conversation stays on the target that served the first turn. Conversations with no stored state keep round-robin over both targets.                                                                                                                                                                                                                                                                                                                            |
| `intelligent_balancer_routes_by_tradeoff`    | On the `aigw-master` data plane (no published 2.5 image yet), two `intelligent` balancer models over `gpt-4.1-mini` and `gpt-4.1`, with `cost_quality_tradeoff` `cost` and `quality`. The data plane sends the classifier request to the converted `url` and `format` with the converted `header_name` and `header_value`, resolved from an env vault. For a `debugging` task, `cost` selects `gpt-4.1-mini` and `quality` selects `gpt-4.1`. A later turn of one conversation keeps its target and does not ask the classifier again. A classifier failure still gets an answer, with the `general` task type and an `X-Kong-LLM-Classifier-Error` header. The targets need a 1M-token context in the data plane catalog: `gpt-4o-mini` is never eligible. |
| `model_body_size_2_3` and `_2_4`             | The `max_request_body_size` that each model gets on the data plane. Most models in the input set no limit, which is the input after the AI Gateway API stops sending its 8 MB default. On 2.3, every model without a limit keeps 8 MB, from the `ai-model-selector` limit and the `ai-proxy-advanced` schema default. On 2.4, an `openai` model gets 50 MB and an `anthropic` model gets 32 MB. A `mistral` model has no published limit and keeps 8 MB. On both releases, a configured limit stays, including an explicit 8 MB. The admin API shows each loaded limit. A body up to the limit reaches the (mocked) upstream. A body above it does not reach the upstream. When `ai-proxy-advanced` has a lower limit than `ai-model-selector`, it rejects the body with 400 and tells its limit. When the two limits are equal, `ai-model-selector` finds no model, and the request gets 503 from the placeholder Service. |

## Failures

On failure the harness writes debugging artifacts under `e2e/artifacts/<case>/`
(gitignored): the converted config that was mounted and the gateway
container's last 500 log lines.
