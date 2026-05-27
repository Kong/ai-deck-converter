# ai-deck-converter

Translates an **AI Gateway entity-model** YAML configuration into a **Kong
Gateway decK** declarative configuration (`_format_version: "3.0"`).

The AI Gateway model is a higher-level abstraction; its entities are realized in
Kong via Gateway entities plus the AI plugins (`ai-proxy-advanced`,
`ai-mcp-proxy`, `ai-a2a-proxy`). This tool performs that lowering so you can
author at the AI-Gateway level and deploy with decK.

## Install / build

```sh
go build ./cmd/ai-deck-converter
```

## Usage

```sh
# read a file, write Kong decK YAML to stdout
ai-deck-converter input.yaml

# write to a file
ai-deck-converter -o kong.yaml input.yaml

# read from stdin
cat input.yaml | ai-deck-converter -
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `-o` | stdout | Output file path. |
| `-strict` | `false` | Treat unresolved references (unknown provider/policy) as errors instead of warnings. |
| `-label-tag-prefix` | `""` | Prefix prepended to label-derived tags, e.g. `aigw/`. |

Warnings (unresolved references, unsupported features, placeholders) are printed
to stderr; the converted config still goes to stdout/`-o`.

## Library

```go
import "github.com/gperanich/ai-deck-converter/convert"

out, warnings, err := convert.Convert(srcYAML, convert.Options{})
```

`convert.ConvertDocument` is also available if you already hold a parsed
`aigw.Document`.

## Input format

A single YAML document grouping AI Gateway entities by kind. Credentials are
nested under their consumer.

```yaml
models:        [ ... ]   # -> routes (per capability/format) + ai-proxy-advanced + ai-models
providers:     [ ... ]   # folded into ai-proxy-advanced targets (not standalone)
mcp_servers:   [ ... ]   # -> Service + Route + ai-mcp-proxy
agents:        [ ... ]   # -> Service + Route (+ ai-a2a-proxy when type: a2a)
policies:      [ ... ]   # -> Kong plugins (global, or scoped per reference)
consumers:     [ ... ]   # -> consumers (+ nested keyauth_credentials, groups)
consumer_groups: [ ... ] # -> consumer_groups
vaults:        [ ... ]   # -> vaults
```

A Model's `config.route.paths[0]` provides the **base path** (e.g. `/ai`); the
full route paths are derived per capability/format from the endpoint table.

See `convert/testdata/*/input.yaml` for worked examples.

## Entity mapping

| AI Gateway source | Kong decK output |
|---|---|
| Model | One **route per (provider endpoint, capability)** under a single shared `ai-gateway` Service, with the path derived from the model's `formats[0].type` (llm_format) + capability via the endpoint table. Each route gets an `ai-proxy-advanced` plugin (`route:` FK) — models that resolve to the same endpoint share one route, contributing one `targets[]` entry each. Body-model routes also get an `ai-model-selector` plugin. One `ai-models` entry (`name` + `alias`) is emitted per model. |
| Provider | Not a standalone entity. Its `type` and `config.auth` populate each referencing target's `model.provider`, `model.options`, and `auth`. |
| MCP Server | Service + Route + `ai-mcp-proxy` (`config.mode` = source type). Server ACLs / per-tool ACLs are written into the plugin config (`default_acl`, `tools[].acl`), not Kong `acl` plugins. |
| Agent (`a2a`) | Service (`config.url`) + Route + `ai-a2a-proxy` plugin (logging). |
| Agent (`http`) | Service (`config.url`) + Route, no AI plugin. |
| Policy | Kong plugin (`name` = policy `type`, config passed through). `global: true` -> one top-level plugin; otherwise instantiated per referencing entity. |
| Consumer | Consumer (`username` = name, `custom_id`), `groups` membership, nested `keyauth_credentials`, scoped policy plugins. |
| Consumer Group | `consumer_groups` entry + scoped policy plugins. |
| Credential | `keyauth_credentials` nested under the consumer (`key` from `api_key`, `ttl`). |
| Vault | `vaults` entry (`prefix` = name, `name` = backend type, config passed through). |
| Model `policies`/`acls` | Top-level plugins scoped to the `ai-models` entity via a `model:` FK. |
| Agent `acls` | Kong `acl` plugin on the agent's Route. |
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
- **Labels** are lossy as tags when a value contains `:`.
