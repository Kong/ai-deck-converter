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
./ai-deck-converter input.yaml

# Kong decK -> AI Gateway (auto-detected: decK docs carry _format_version)
./ai-deck-converter kong.yaml

# write to a file
./ai-deck-converter -o kong.yaml input.yaml

# read from stdin
cat input.yaml | ./ai-deck-converter -

# force a direction
./ai-deck-converter -direction from-deck kong.yaml

# emit Koko-style db-less output
./ai-deck-converter -direction to-dbless input.yaml
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `-o` | stdout | Output file path. |
| `-direction` | `auto` | Conversion direction: `auto`, `to-deck` (AI Gateway → decK), `to-dbless` (AI Gateway → db-less), or `from-deck` (decK → AI Gateway). Auto-detection keys off `_format_version`, which only decK documents carry. |
| `-strict` | `false` | Treat unresolved references and unconvertible entities as errors instead of warnings. |
| `-label-tag-prefix` | `""` | Prefix for label-derived tags, e.g. `aigw/` (prepended when converting to decK, stripped when reverting). |

Warnings (unresolved references, unsupported features, placeholders, dropped
entities) are printed to stderr; the converted config still goes to stdout/`-o`.

## Library

```go
import (
    "github.com/Kong/ai-deck-converter/convert"
    "github.com/Kong/ai-deck-converter/revert"
)

// AI Gateway -> Kong decK
out, warnings, err := convert.Convert(srcYAML, convert.Options{})

// Kong decK -> AI Gateway
out, warnings, err = revert.Revert(deckYAML, revert.Options{})
```

`convert.ConvertDocument` / `revert.RevertDocument` are also available if you
already hold a parsed `aigw.Document` / `kong.Document`.

## Input format

A single YAML document grouping AI Gateway entities by kind. Credentials are
nested under their consumer.

```yaml
models:          [ ... ] # -> routes (per capability/format) + ai-proxy-advanced + ai-models
model_providers: [ ... ] # folded into ai-proxy-advanced targets (not standalone)
mcp_servers:     [ ... ] # -> Service + Route + ai-mcp-proxy
agents:          [ ... ] # -> Service + Route (+ ai-a2a-proxy when type: a2a)
policies:        [ ... ] # -> Kong plugins (global, or scoped per reference)
consumers:       [ ... ] # -> consumers (+ nested keyauth_credentials, groups)
consumer_groups: [ ... ] # -> consumer_groups
vaults:          [ ... ] # -> vaults
```

A Model's `config.route.paths[0]` provides the **base path** (e.g. `/ai`); the
full route paths are derived per capability/format from the endpoint table.

See `convert/testdata/*/input.yaml` for worked examples.

## Entity mapping

| AI Gateway source | Kong decK output |
|---|---|
| Model | One **route per (provider endpoint, capability)** under a single shared `ai-gateway` Service, with the path derived from the model's `formats[0].type` (llm_format) + capability via the endpoint table. Each route gets an `ai-proxy-advanced` plugin (`route:` FK) — models that resolve to the same endpoint share one route, contributing one `targets[]` entry each. Body-model routes also get an `ai-model-selector` plugin. One `ai-models` entry (`name` + `alias`) is emitted per model. |
| Provider | Not a standalone entity. Its `type` and `config.auth` populate each referencing target's `model.provider`, `model.options`, and `auth`. |
| MCP Server | Service + Route + `ai-mcp-proxy` (`config.mode` = source type). Server ACLs / per-tool ACLs are written into the plugin config (`default_acl`, `tools[].acl`), not Kong `acl` plugins. `access.identity_providers` + `access.metadata` (openid-connect) add an `ai-mcp-oauth2` plugin and append `metadata.endpoint` to the route (listener / conversion-listener / passthrough-listener only); a `key-auth` provider adds a `key-auth` plugin (and is rejected if `metadata` is set). `config.upstream.auth` (AWS SigV4) lowers to the plugin's `auth` record. |
| Agent (`a2a`) | Service (`config.url`) + Route + `ai-a2a-proxy` plugin (logging). `config.upstream.auth` (AWS SigV4) lowers to the plugin's `auth` record, and `config.proxy` to `proxy_config`. |
| Agent (`http`) | Service (`config.url`) + Route, no AI plugin. |
| Policy | Kong plugin (`name` = policy `type`, config passed through). `global: true` -> one top-level plugin; otherwise instantiated per referencing entity. |
| Consumer | Consumer (`username` = name, `custom_id`), `groups` membership, nested `keyauth_credentials`, scoped policy plugins. |
| Consumer Group | `consumer_groups` entry + scoped policy plugins. |
| Credential | `keyauth_credentials` nested under the consumer (`key` from `api_key`, `ttl`). |
| Vault | `vaults` entry (`prefix` = name, `name` = backend type, config passed through). |
| Model `policies`/`acls` | Top-level plugins scoped to the `ai-models` entity via a `model:` FK. |
| Agent `access.acls` | Kong `acl` plugin on the agent's Route. |
| `labels` | `tags` flattened to sorted `key:value` strings. |

### Capability → endpoint

A model's `capabilities` choose which routes are created. The mapping (path,
methods, `route_type`, `genai_category`) is defined per provider section in
`convert/endpoints.go`, derived from `ref/supported-endpoints.md`. Loose
spellings are normalized (`chat`→`generate`, `batch`→`batches`); bare `audio`
fans out to speech/transcription/translation. Native formats (bedrock, gemini,
vertex) emit regex routes (`~/ai/...`); capabilities that share an upstream
endpoint (e.g. bedrock embeddings/image/audio/video → `/invoke`) collapse into
one route with multiple targets.

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
  plugin → `access.metadata` + a synthesized openid-connect provider (the
  `.well-known` path is stripped back off the route), and `key-auth` /
  `openid-connect` → `access.identity_providers`.
- **Unknown plugins → Policies** (global when top-level and unscoped, otherwise
  referenced from the owning entity); `acl` plugins → `acls`.
- **Anything unconvertible is warned about and dropped**; `-strict` makes those
  drops fatal.

Lossy by design (the forward direction never emits them): `display_name`,
`enabled`, original provider names, capability spellings (`chat` comes back as
`generate`; bare `audio` stays fanned out), and `formats` beyond the first.

## Assumptions and limitations

- **`ai-models` / `ai-model-selector`.** Output uses the `ai-models` entity and
  `ai-model-selector` plugin shown in `ref/examples/models/`. If your decK/Kong
  build doesn't recognize these yet, sync the rest and add them when available.
- **Shared gateway Service.** All model routes nest under one `ai-gateway`
  Service with the nominal url `http://ai-gateway.upstream.local`;
  `ai-proxy-advanced` overrides the real upstream per target.
- **One primary endpoint per capability.** Each (section, capability) maps to a
  single canonical endpoint. `rerank` has no OpenAI-format `route_type`
  (native-only) and falls back to `llm/v1/chat`.
- **Multi-modal routes.** When several capabilities share one upstream endpoint
  (e.g. bedrock `/invoke`), the route's `genai_category` is taken from the first
  contributor (a plugin-level field can hold only one value).
- **Credentials.** Only `api-key` (`keyauth_credentials`) is generated; other
  credential types are warned about and skipped.
- **MCP upstream.** Passthrough MCP servers without an `upstream_url` get a
  placeholder host and a warning.
- **Upstream auth.** Agents and MCP Servers carry `config.upstream.auth` (AWS
  SigV4, `type: aws`), which maps to the `ai-a2a-proxy` / `ai-mcp-proxy` `auth`
  record (`provider: aws_iam`, nested `aws_iam` options). Unsupported auth types
  are warned about and dropped. The plugin's `aws_iam.bearer_token` has no AI
  Gateway representation, so the reverse direction warns and drops it.
- **MCP OAuth2.** MCP `access.identity_providers` / `access.metadata` round-trips
  in both directions. An openid-connect provider lowers its client credentials
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

## Community

Please review our [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community guidelines.
