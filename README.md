# ai-deck-converter

Translates between an **AI Gateway entity-model** YAML configuration and a
**Kong Gateway decK** declarative configuration (`_format_version: "3.0"`), in
either direction.

The AI Gateway model is a higher-level abstraction; its entities are realized in
Kong via Gateway entities plus the AI plugins (`ai-proxy-advanced`,
`ai-mcp-proxy`, `ai-a2a-proxy`). This tool performs that lowering so you can
author at the AI-Gateway level and deploy with decK — and the reverse lifting,
so an existing decK config (including hand-written ones) can be recovered into
the AI Gateway entity model.

## Install / build

```sh
make build
```

## Usage

```sh
# AI Gateway -> Kong decK (direction auto-detected from the input)
./ai-deck-converter -min-runtime-version 2.1 input.yaml

# Kong decK -> AI Gateway (auto-detected: decK docs carry _format_version)
./ai-deck-converter kong.yaml

# write to a file
./ai-deck-converter -min-runtime-version 2.1 -o kong.yaml input.yaml

# read from stdin
cat input.yaml | ./ai-deck-converter -min-runtime-version 2.1 -

# force a direction
./ai-deck-converter -direction from-deck kong.yaml

# emit Koko-style db-less output
./ai-deck-converter -direction to-dbless -min-runtime-version 2.1 input.yaml
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `-o` | stdout | Output file path. |
| `-min-runtime-version` | none | Oldest AI Gateway release (`MAJOR.MINOR`, e.g. `2.1`) that must run the output. Required for `to-deck` and `to-dbless`; not valid with `from-deck`. Same value as the Konnect `min_runtime_version` field. See [Minimum runtime version](#minimum-runtime-version). |
| `-direction` | `auto` | Conversion direction: `auto`, `to-deck` (AI Gateway → decK), `to-dbless` (AI Gateway → db-less), or `from-deck` (decK → AI Gateway). Auto-detection keys off `_format_version`, which only decK documents carry. |
| `-strict` | `false` | Exit non-zero on warnings too. For `to-deck`/`to-dbless`, any issue fails the run. For `from-deck`, unresolved references and unconvertible entities become errors. |
| `-label-tag-prefix` | `""` | Prefix for label-derived tags, e.g. `aigw/` (prepended when converting to decK, stripped when reverting). |

Conversion issues are printed to stderr, one per line, as
`SEVERITY CODE kind "name" [fields]: reason`. The converted config always goes
to stdout/`-o`. The exit code is 1 when an issue has the `ERROR` severity, or on
any issue with `-strict`. The reverse direction prints `warning:` lines.

## Minimum runtime version

Every forward conversion needs the oldest AI Gateway release that must run the
output, as `MAJOR.MINOR`. It is the `min_runtime_version` of the Konnect AI
Gateway. The converter supports `2.0` to `2.3`.

- An empty value, a value that is not `MAJOR.MINOR` (for example `2.1.0` or
  `latest`), or a value below `2.0` fails the run.
- A value above `2.3` gives a `WARNING` and converts for `2.3`.
- A feature that needs a newer release gives an
  `UNSUPPORTED_IN_MIN_RUNTIME_VERSION` issue. The rules in
  `internal/aimap/versions.go` decide whether the field is dropped with a
  `WARNING` or the entity is left out with an `ERROR`. See
  `docs/decisions/00225-dataplane-conversion-compatability.md`.

## Conversion issues

`convert.Convert` returns a `ConversionResult`: the Kong document, its
metadata, and every issue found during the run. It returns an `error` only when
the whole run fails, for example on invalid YAML. The caller decides which
issues stop its workflow. See
[RFC 00207](docs/decisions/00207-conversion-issue-reporting.md) for the design.

Each `ConversionIssue` has a `Code`, a `Severity`, a `Source`, and a `Reason`:

- **Severity** is `WARNING` (the converter worked around the issue) or `ERROR`
  (the output is invalid or diverges from the user intent).
- **Code** is one of `MISSING_REQUIRED_CONFIG_FIELD`, `UNDEFINED_REFERENCE`,
  `CONFIG_FIELD_OVERRIDDEN`, `CONFIG_FIELD_DROPPED`, `CONFIG_FIELD_IGNORED`,
  `CONFIG_FIELD_INVALID`, `BEHAVIOR_MAY_DIFFER`, or `INTERNAL_ERROR`. One code can
  occur with either severity. Handle an unknown code by its severity.
- **Source** names the entity: `Kind` is the singular form of its top-level
  document key (`model`, `mcp_server`, `agent`, `policy`, `custom_policy`,
  `auth_strategy`, `consumer`, `consumer_group`, `sni`, ...). `Fields` are
  paths relative to the entity, such as `targets[0].provider`.
- **Reason** is a human-readable message. It is not API, so do not parse it.

The converter continues after every issue. These rules decide what it emits:

- An entity that cannot be converted is left out with an `ERROR`. The other
  entities still convert. A panic in one entity gives `INTERNAL_ERROR`.
- A nested item that cannot be converted, such as an MCP tool without a
  `description` or a model target with an unknown provider, is left out with a
  `WARNING`. A model whose targets are all left out is itself left out with an
  `ERROR`.
- An entity that references an unknown or unconvertible auth strategy is left
  out, so it is never generated unprotected.
- A global or referenced policy that cannot be converted is left out with an
  `ERROR`. The entities that reference it still convert.
- When two models conflict, for example two passthrough models on one path,
  the later model in the document is left out.

## Library

```go
import (
    "github.com/Kong/ai-deck-converter/convert"
    "github.com/Kong/ai-deck-converter/revert"
)

// AI Gateway -> Kong decK
result, err := convert.Convert(srcYAML, convert.Options{MinRuntimeVersion: "2.1"})
for _, issue := range result.Issues {
    if issue.Severity == convert.Error { /* stop, or report */ }
}
deckYAML, err := result.Output.ToYAML()     // or result.Output.ToDBLess().ToYAML()
metadata := result.Metadata                 // or result.Output.ToDBLess().Metadata()

// Kong decK -> AI Gateway
aigwYAML, warnings, err := revert.Revert(deckYAML, revert.Options{})
```

`revert.RevertDocument` is also available if you already hold a parsed
`kong.Document`.

## Input format

A single YAML document grouping AI Gateway entities by kind. Credentials are
nested under their consumer.

```yaml
models:          [ ... ] # -> routes (per capability/format) + ai-proxy-advanced + ai-models
model_providers: [ ... ] # folded into ai-proxy-advanced targets (not standalone)
mcp_servers:     [ ... ] # -> Service + Route + ai-mcp-proxy
agents:          [ ... ] # -> Service + Route (+ ai-a2a-proxy when type: a2a)
policies:        [ ... ] # -> Kong plugins (global, or scoped per reference)
auth_strategies: [ ... ] # -> key-auth / openid-connect plugins on guarded routes
consumers:       [ ... ] # -> consumers (+ nested keyauth_credentials, groups)
consumer_groups: [ ... ] # -> consumer_groups
vaults:          [ ... ] # -> vaults
ca_certificates: [ ... ] # -> ca_certificates
```

A Model's `config.route.paths[0]` provides the **base path** (e.g. `/ai`); the
full route paths are derived per capability/format from the endpoint table.

`identity_providers` is the former name of `auth_strategies`, as the top-level
key and as the `access` reference list on models, agents, and MCP servers. The
converter rejects it with an error. Use `auth_strategies`.

See `convert/testdata/*/input.yaml` for worked examples.

## Entity mapping

| AI Gateway source | Kong decK output |
|---|---|
| Model | One **route per (provider endpoint, capability)** under a single shared `ai-gateway` Service, with the path derived from the model's `formats[0].type` (llm_format) + capability via the endpoint table. Each route gets an `ai-proxy-advanced` plugin (`route:` FK) — models that resolve to the same endpoint share one route, contributing one `targets[]` entry each. Body-model routes also get an `ai-model-selector` plugin. One `ai-models` entry (`name` + `alias`) is emitted per model — one per `config.route.model.values` entry when the model declares several aliases, each with its own `ai-proxy-advanced` (and policy/ACL) copy scoped to that alias, so requests naming any alias select the model. |
| Provider | Not a standalone entity. Its `type` and `config.auth` populate each referencing target's `model.provider`, `model.options`, and `auth`. |
| MCP Server | Service + Route + `ai-mcp-proxy` (`config.mode` = source type). Server ACLs / per-tool ACLs are written into the plugin config (`default_acl`, `tools[].acl`), not Kong `acl` plugins. `access.auth_strategies` + `access.metadata` (openid-connect) add an `ai-mcp-oauth2` plugin and append `metadata.endpoint` to the route (listener / conversion-listener / passthrough-listener only); a `key-auth` strategy adds a `key-auth` plugin (and is rejected if `metadata` is set). `config.upstream.auth` (AWS SigV4) and the top-level `token_vault` lower to the plugin's `auth` record (mutually exclusive). A `conversion-only` server serves no MCP traffic of its own and cannot declare `access` itself, so its route gets a `pre-function` gate (tagged `aigw-generated:mcp-toolset-gate`) that answers 404 to any request not arriving over the unix socket `ai-mcp-proxy` re-enters Kong through to execute a listener's tool call: its tools are reachable only through the listeners that name it in `config.sources`, on their access terms. The gate is dropped again on revert. A `conversion-only` server that no listener names is dropped entirely, with a warning: it reaches no client. `key-auth` access on an MCP server is always emitted with `hide_credentials: false`, so the client's key survives onto the internal request `ai-mcp-proxy` issues when executing a tool. |
| Agent (`a2a`) | Service (`config.url`) + Route + `ai-a2a-proxy` plugin (logging). `config.upstream.auth` (AWS SigV4) lowers to the plugin's `auth` record, and `config.proxy` to `proxy_config`. |
| Agent (`http`) | Service (`config.url`) + Route, no AI plugin. |
| MCP Server / Agent `config.certificates`, `config.ca_certificates` | Set the generated Service's `client_certificate` and `ca_certificates` to the IDs of the named Certificate and CA Certificates. A Service presents one client certificate, so only the first entry of `certificates` is used. A referenced entity without an `id` gets the `id` that `db-less` mode derives for it. The fields are ignored with a warning if the Service URL is not `https`, because Kong rejects them there. An unknown name warns and is dropped; `-strict` makes it an error. |
| Policy | Kong plugin (`name` = policy `type`, config passed through). `global: true` -> one top-level plugin; otherwise instantiated per referencing entity. |
| Datastore | Not a standalone entity. Referenced by name from a Policy's `datastores` field, or from a Model's `datastores` field (only when that model's balancer is `semantic`); its `config` is merged into that policy's plugin config at a path fixed by the plugin's type (`config.redis` for most; `config.storage_config.redis` for `acme`; `config.resources.cache.redis` for `datakit`; `config.vectordb.<redis\|pgvector>` for the VectorDB family, sub-block and `strategy` chosen by the Datastore's `type`). On a Model it replaces the connection sub-block of ai-proxy-advanced `config.vectordb` — the model keeps its own `dimensions`/`distance_metric`/`threshold`, and only `redis-ee` or `vectordb` types are accepted. Rejected if the plugin type doesn't support a Datastore, or if the Datastore's `type` isn't one that plugin accepts. An unknown reference gives an `UNDEFINED_REFERENCE` error, and the Policy or Model converts without the connection. |
| Auth Strategy | Kong authentication plugin (`name` = strategy `type`: `key-auth` or `openid-connect`, config passed through) on the route of each entity whose `access.auth_strategies` references it, with `config.anonymous` pointing at a synthesized `anonymous` consumer that a `request-termination` plugin rejects with a 401. A model route with one auth strategy has no `anonymous` fallback: the auth plugin rejects the request itself. Models with different auth-strategy sets never share a route. |
| Consumer | Consumer (`username` = name, `custom_id`), `groups` membership, nested `keyauth_credentials`, scoped policy plugins. |
| Consumer Group | `consumer_groups` entry + scoped policy plugins. |
| Credential | `keyauth_credentials` nested under the consumer (`key` from `api_key`, `ttl`). |
| Vault | `vaults` entry (`prefix` = name, `name` = backend type, config passed through). |
| CA Certificate | `ca_certificates` entry (`cert`, `cert_digest`); `name` preserved as an `ai-gateway-name:` tag (Kong's entity has no name field). |
| Certificate | `certificates` entry (`cert`/`key` and the optional `cert_alt`/`key_alt` pair passed through). Kong certificates have no `name`, so the source name is not represented in the output; in `db-less` mode it still seeds the generated `id`. |
| SNI | Nested under its referenced Certificate's `snis` list (`hostname` → `name`, `labels` → `tags`); decK's file format has no standalone SNI entity. The SNI's own `name`/`display_name` have no Kong counterpart and are dropped. In `db-less` mode SNIs become a top-level `snis` entry referencing the certificate's generated `id`. |
| Model with `formats: [{type: passthrough}]` | Requires AI Gateway 2.2+. **One route on the bare base path** (every method, no endpoint suffix), whatever `capabilities` it declares — `[]` is fine — with one route-scoped `ai-proxy-advanced` whose targets carry `route_type: passthrough` and no `model_alias`; `llm_format` is the provider's native format (openai for providers without their own). No `ai-model-selector`, no video lifecycle routes, and the model's policies/ACLs are route-scoped rather than `model:`-scoped, since nothing can select an `ai-models` row for a body of unknown shape (the row is still emitted). No `anthropic_version` default is applied. |
| Model `policies`/`acls` | Top-level plugins scoped to the `ai-models` entity via a `model:` FK. When a policy's plugin type has a static Kong priority above `ai-model-selector`'s (`aimap.OutranksModelSelector`, e.g. `pre-function`), the route's `ai-model-selector` gets `ordering.before.access` with that plugin name. See "Assumptions and limitations" below. |
| Agent `access.acls` | Kong `acl` plugin on the agent's Route. |
| `labels` | `tags` flattened to sorted `key:value` strings. |

### Capability → endpoint

A model's `capabilities` choose which routes are created. The mapping (path,
methods, `route_type`, `genai_category`) is defined per provider section in
`convert/endpoints.go`, derived from `ref/supported-endpoints.md`. Loose
spellings are normalized (`chat`→`generate`, `batch`→`batches`); bare `audio`
fans out to speech/transcription/translation. Native formats (bedrock, gemini)
emit regex routes (`~/ai/...`); capabilities that share an upstream
endpoint (e.g. bedrock embeddings/image/audio/video → `/invoke`) collapse into
one route with multiple targets.

Most capabilities map to a single canonical endpoint, but a few are reachable
through more than one and get a route per endpoint: bedrock `generate` emits
both `bedrock-converse` (Converse) and `bedrock-invoke` (InvokeModel), each
carrying the model's target(s). Likewise every gemini capability is served on
both its Gemini API path and its Gemini Enterprise path (e.g. `gemini-generate`
and `gemini-enterprise-generate`), whichever provider type serves it; the Gemini
Enterprise-only
image (`:predict`), video (`:predictLongRunning`) and rerank (`:rank`)
capabilities are available to gemini-format models too. These extra endpoints live in the capability's
own `internal/aimap.EndpointTable` entry, as `EndpointEntry.Secondary`
alongside the canonical `Primary` spec — read both together via
`aimap.EndpointsFor` / `aimap.SectionEndpoints` — never look up a capability's
endpoint(s) any other way, or the two directions can drift. Bedrock's invoke
spec for `generate` is reused verbatim from `audio/speech` (the same
endpoint), so a model declaring both capabilities gets only one
`bedrock-invoke` route/target, not two; see "Multi-modal routes" below for the
resulting revert-side ambiguity.

## Reverse direction (decK → AI Gateway)

The `revert` package lifts a decK config back into the entity model. It is
**best-effort generic**: recognition is by AI plugin (`ai-proxy-advanced`,
`ai-mcp-proxy`, `ai-a2a-proxy`) anywhere in the config, with the forward
converter's conventions (route names, `ai-models` entries) used as hints when
present. Converter-produced configs round-trip byte-for-byte
(`revert/roundtrip_test.go` enforces this for every forward golden case).

How Kong entities come back:

- **ai-proxy-advanced routes → Models.** Targets group into one Model per
  `model_alias` (the `ai-models` entry supplies the name); capabilities are
  recovered from the route name / `route_type` / `genai_category` / path shape
  via the shared endpoint table, and the base path is recovered from the route
  path. Alias-less targets fall back to positional `ai-models` matching, then
  the route name.
- **Providers are synthesized** from each target's auth/options (deduped by
  fingerprint) since Kong has no standalone provider entity. Names derive from
  the vault prefix in the credential (`openai-env`) or a per-type counter.
- **ai-mcp-proxy → MCP Servers**, **ai-a2a-proxy → a2a Agents**, plain
  services with routes → **http Agents**. On an MCP route, an `ai-mcp-oauth2`
  plugin → `access.metadata` + a synthesized openid-connect auth strategy (the
  `.well-known` path is stripped back off the route), and `key-auth` /
  `openid-connect` → `access.auth_strategies`.
- **`key-auth` / `openid-connect` plugins → Auth Strategies**, deduped by
  (type, config minus the synthesized `anonymous` fallback) and named
  `<type>-<n>` (`openid-connect-1`), emitted under `auth_strategies:` and
  referenced from the owning entity's `access.auth_strategies`.
- **`certificates` → Certificates**, passed straight back through. Kong has no
  certificate name, so one is synthesized positionally (`certificate-1`); the
  name is absent from the decK output, so this never changes a round trip.
- **Service `client_certificate` / `ca_certificates` → `config.certificates` /
  `config.ca_certificates`** on the MCP Server or Agent. Each ID becomes the
  name of the matching reverted Certificate or CA Certificate.
- **Certificate `snis` → SNIs**, one AI Gateway SNI per nested entry. Both the
  SNI name and its `certificate` reference are synthesized positionally
  (`sni-1`, `certificate-1`, ...), so this never changes a round trip either.
- **Unknown plugins → Policies** (global when top-level and unscoped, otherwise
  referenced from the owning entity); `acl` plugins → `acls`.
- **Anything unconvertible is warned about and dropped**; `-strict` makes those
  drops fatal.

Lossy by design (the forward direction never emits them): `display_name`,
`enabled`, original provider names, certificate names, SNI names, capability
spellings (`chat` comes back as `generate`; bare `audio` stays fanned out),
and `formats` beyond the first.

## Assumptions and limitations

- **`ai-models` / `ai-model-selector`.** Output uses the `ai-models` entity and
  `ai-model-selector` plugin shown in `ref/examples/models/`. If your decK/Kong
  build doesn't recognize these yet, sync the rest and add them when available.
- **Shared gateway Service.** All model routes nest under one `ai-gateway`
  Service with the nominal url `http://ai-gateway.upstream.local`;
  `ai-proxy-advanced` overrides the real upstream per target.
- **Realtime routes use WebSocket.** Kong selects its WebSocket proxy path from
  the Service protocol, so `realtime` routes nest under a second
  `ai-gateway-websocket` Service (`ws://ai-gateway.upstream.local`). They use
  `ws`/`wss` (an authored `http`/`https` maps to `ws`/`wss`) and no methods,
  and each model gets its own realtime route. `ai-model-selector` does not run
  on WebSocket, so the realtime route's `ai-proxy-advanced`, policy, ACL, and
  auth-strategy plugins are route-scoped and carry the route's `protocols`, and
  its targets carry no `model_alias`. Policy plugins whose schema rejects
  `ws`/`wss` (every AI policy plugin, such as `ai-prompt-guard`) are left off
  the realtime route with a warning; they still apply to the model's HTTP
  routes. The anonymous consumer's `request-termination` cannot run on
  WebSocket either, so a realtime auth plugin has no `anonymous` fallback, a
  model with more than one auth strategy cannot declare `realtime`. A global
  authentication policy cannot protect a WebSocket route either, so it leaves
  out every model with the `realtime` capability. The global policy stays.
- **Mostly one endpoint per capability.** Each (section, capability) maps to an
  `aimap.EndpointEntry` with a primary canonical endpoint, plus any secondaries
  in its `Secondary` field (currently just bedrock `generate`, also served by
  `/invoke`). `rerank` has no OpenAI-format `route_type` (native-only) and
  falls back to `llm/v1/chat`.
- **Multi-modal routes.** When several capabilities share one upstream endpoint
  (e.g. bedrock `/invoke`), the route's `genai_category` is taken from the first
  contributor (a plugin-level field can hold only one value). Bedrock's
  `generate` invoke spec is identical to `audio/speech`'s, so on revert a
  `bedrock-invoke` route/target with no other distinguishing signal comes back
  as `audio/speech` (alphabetically first), even if it was originally created
  by a `generate` capability — see `convert/testdata/58_bedrock_generate_and_speech`.
  This never breaks round-tripping a converter-produced config, since re-
  converting the recovered model reproduces the same route/target either way.
- **Bedrock mantle targets.** A `bedrock` target with `endpoint_type: mantle`
  serves only the `llm/v1/chat`, `llm/v1/responses`, and `llm/v1/files` route
  types, in the `openai` or `anthropic` format. It therefore gets only the
  `openai` `generate`, `agentic`, and `files` endpoints and the `anthropic`
  `generate` endpoint. On any other endpoint of the model the target is left
  out with a `BEHAVIOR_MAY_DIFFER` warning. The model fails when no target
  serves a capability. The `bedrock` format has no `responses` endpoint. The
  data plane rejects `llm/v1/responses` for it.
- **Credentials.** Only `api-key` (`keyauth_credentials`) is generated; other
  credential types are warned about and skipped.
- **High-priority model policies reorder `ai-model-selector`.** A model
  policy can outrank `ai-model-selector`, for example `pre-function`. Without
  reordering, it runs before Kong sets `ngx.ctx.ai_model`, so Kong skips it.
  An `ordering` block on the model-scoped policy has no effect. Kong reads
  `ordering` before the model is known, and a model-scoped config is not
  visible then. The converter adds the policy's plugin name to the route's
  `ai-model-selector` `ordering.before.access` instead. Side effects:
  - `ordering` matches plugin names, not instances. On that route, every
    instance of the named plugin runs after `ai-model-selector`. This
    includes instances that are not model-scoped.
  - Plugins on that route that the rule does not name can then run after
    `ai-model-selector`. Authentication is not one of them. When the route
    has auth strategies, the converter also adds the auth plugins to
    `ordering.after.access`. So an unauthenticated request gets its 401
    before `ai-model-selector` reads the body and before the policy runs.
    Kong orders by plugin name. An auth strategy must have type `key-auth`
    or `openid-connect`, and a model policy cannot have either type, so no
    plugin name is in both lists.
  - A model route with one auth strategy has no `anonymous` fallback. The
    auth plugin rejects the request itself, so `request-termination` stays
    out of the ordering. A model-scoped `request-termination` policy (for
    example, one that blocks a model) keeps its low priority and runs after
    `ai-model-selector`.
  - A route with more than one auth strategy needs the fallback: a request
    passes when any strategy accepts it. Only the `anonymous` consumer's
    `request-termination` then rejects it, so the converter also adds
    `request-termination` to `ordering.after.access`. A model-scoped
    `request-termination` policy on that route is then skipped: Kong checks
    it before the model is known. The converter warns about it. Scope it to
    the route or globally instead.
  - Any `ordering` in a workspace turns on dynamic plugin ordering for the
    whole workspace. Dynamic ordering needs an Enterprise license.
  - A user `ordering` that puts a plugin before `ai-model-selector` makes a
    cycle. Kong then logs an error and uses the static priorities.

  `e2e/testdata/pre_function_model_ordering`,
  `e2e/testdata/pre_function_model_ordering_auth`, and
  `e2e/testdata/model_scoped_request_termination_auth` prove this against a
  real gateway.
- **MCP upstream.** Passthrough MCP servers without an `upstream_url` get a
  placeholder host and a warning.
- **Upstream auth.** Agents and MCP Servers carry `config.upstream.auth` (AWS
  SigV4, `type: aws`), which maps to the `ai-a2a-proxy` / `ai-mcp-proxy` `auth`
  record (`provider: aws_iam`, nested `aws_iam` options). An unsupported auth
  type leaves out the agent or MCP server with an error. The plugin's `aws_iam.bearer_token` has no AI
  Gateway representation, so the reverse direction warns and drops it.
- **Token Vault.** An MCP Server's top-level `token_vault` (`directory`,
  `provider`, optional `redis` + `encryption_secrets`) lowers to the
  `ai-mcp-proxy` `auth` record (`provider: token_vault`, nested `token_vault`
  options) and round-trips in both directions. The nested
  `redis` block (Konnect's `AIGatewayRedisCloudConfiguration` shape) flattens
  into the plugin redis config's prefixed keys (`keepalive.pool_size` →
  `keepalive_pool_size`, `sentinel.master` → `sentinel_master`,
  `cluster.nodes` → `cluster_nodes`, `cloud_authentication.type` →
  `auth_provider` with provider-prefixed fields, …). `token_vault` and
  `config.upstream.auth` are mutually exclusive (both lower to the same plugin
  auth record) — declaring both is a hard error, as is `redis` without
  `encryption_secrets` (the plugin's own entity check).
- **MCP rate limiting.** An MCP Server's top-level `rate_limiting` (`limits[]`
  with `method` or `tool`, `limit`, `window_size`) is copied 1:1 to the
  `ai-mcp-proxy` `config.rate_limiting` and round-trips in both directions. It
  requires runtime 2.3 and is valid only on `conversion-listener` and
  `passthrough-listener`. On any other type, convert leaves out the MCP server
  with an error, and revert drops the field with a warning. The plugin
  validates the values.
- **MCP OAuth2.** MCP `access.auth_strategies` / `access.metadata` round-trips
  in both directions. An openid-connect auth strategy lowers its client credentials
  plus the identically-typed / unambiguous fields onto the `ai-mcp-oauth2`
  plugin: `client_alg`, `client_auth`, `introspection_endpoint`,
  `mtls_introspection_endpoint`, `cache_introspection`, `jwks_endpoint`,
  `leeway` (→ `jwt_claims_leeway`), `ssl_verify`, `consumer_by`,
  `consumer_claims` (→ `consumer_claim`), `consumer_optional`,
  `consumer_groups_claim`, `consumer_groups_optional`, `credential_claim`,
  `keepalive`, `timeout`, and `http_version`. OIDC's flat `http(s)_proxy*` /
  `no_proxy` fields lower to the plugin's structured `proxy_config` record;
  this requires a shared proxy scheme and Basic credentials, because the target
  has one shared credential pair. The reverse reconstructs an equivalent OIDC
  proxy configuration. The reverse synthesizes an openid-connect provider carrying exactly
  those fields; array-valued OIDC fields collapsed to a plugin scalar
  (`client_id`, `client_secret`, `client_alg`, `client_auth`) or single path
  (`consumer_claim`) are re-wrapped into a one-element array, so a multi-element
  source is lossy in the intermediate model but re-converts byte-identically.
  Two further fields map by semantic derivation rather than a plain copy:
  the provider's `hide_credentials: false` becomes the plugin's
  `passthrough_credentials: true` (logical inverse; defaults agree, so only the
  non-default is emitted), and `insecure_relaxed_audience_validation` is
  **always** emitted to mirror OIDC audience enforcement — `false` when the
  provider's `audience_required` is set, `true` otherwise (the reverse
  reconstructs `audience_required` from the metadata `resource`, its RFC 8707
  audience, when the flag is `false`). Deliberately **not** mapped (documented
  non-conversions): `token_exchange`
  (a false friend — OIDC's legacy grant vs the plugin's RFC-8693 upstream
  exchange object), `client_jwk` (OIDC JWK object array vs plugin serialized
  string), `introspection_endpoint_auth_method` (would collide with
  `client_auth`), the downstream/upstream header-mapping fields,
  `tls_client_auth_cert_id` (cert-entity UUID vs inline PEM), and
  `extra_jwks_uris` (no plugin target). Provider config the plugin doesn't carry
  (e.g. `cache_tokens_salt`, `auth_methods`) is not recovered, and
  `authorization_servers`/`scopes_supported` are always attributed to the
  metadata rather than the provider's issuer/scopes.
- **Labels** are lossy as tags when a value contains `:`.
- **Passthrough models.** A passthrough model cannot share its base path with
  another passthrough model, be combined with another format, or use the
  `semantic` balancer. Each leaves out the model with an error. A databricks
  target without `upstream_url` is left out with a warning. Policies that read the normalized LLM shape (guardrails,
  prompt decorators/templates/compressor, RAG injector, semantic cache,
  LLM-as-judge, and `ai-sanitizer` when `anonymize` includes credentials,
  which is its default) produce a
  warning when attached to the model or global; other AI policies work on raw
  bytes, and consumer/consumer-group policies are not checked. A target whose
  provider has no native `llm_format` (azure, mistral, sagemaker, …) also
  warns: usage and cost extraction may find nothing. Revert recovers the model
  with no `capabilities`.

## Community

Please review our [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community guidelines.
