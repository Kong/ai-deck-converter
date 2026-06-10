# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go CLI/library that converts in **both directions** between a high-level **AI Gateway entity-model** YAML config and a **Kong Gateway decK** declarative config (`_format_version: "3.0"`). The AI Gateway model (models, providers, mcp_servers, agents, policies, consumers, vaults) is realized in Kong as Gateway entities plus the AI plugins (`ai-proxy-advanced`, `ai-model-selector`, `ai-mcp-proxy`, `ai-a2a-proxy`). The forward direction (`convert`) lowers AI Gateway → decK; the reverse (`revert`) lifts decK → AI Gateway, best-effort for hand-written configs. See `README.md` for the full entity-mapping table and known limitations.

## Commands

```sh
go build ./cmd/ai-deck-converter      # build the CLI binary
go test ./...                          # run all tests
go test ./convert -run 'TestGolden/01_single_model'   # run one forward golden case
go test ./convert -run TestGolden -update             # regenerate ALL forward golden expected.yaml
go test ./revert -run TestGolden -update              # regenerate ALL reverse golden expected.yaml
go test ./revert -run TestRoundTrip                   # forward→reverse→forward must be byte-identical
```

Run the tool: `ai-deck-converter input.yaml` (stdout), `-o kong.yaml` to write a file, `-` for stdin. The direction is auto-detected (`_format_version` present → decK→AI Gateway) and can be forced with `-direction to-deck|to-dbless|from-deck`. Flags: `-strict` (unresolved refs / unconvertible entities become errors), `-label-tag-prefix` (namespaces label-derived tags; stripped on revert).

## Architecture

One direction of dependency (`convert`/`revert` → `internal/*`):

- **`internal/aigw`** — the AI Gateway entity model. Structs that `aigw.Parse` unmarshals (and the reverter marshals — fields carry `omitempty`; `Balancer`/`ProviderRef`/`TargetModelConfig` have paired Unmarshal/MarshalYAML). One file per entity kind (`model.go`, `provider.go`, `mcp.go`, …). Credentials nest under consumers.
- **`internal/kong`** — the decK model. Structs (`deck.go`) shaped to marshal directly into the YAML decK expects: `_format_version`, nested routes/plugins, and name-based foreign keys (`Ref` → `{name: <x>}`). `Plugin` carries optional `Service`/`Route`/`Consumer`/`ConsumerGroup`/`Model` refs; set one to scope a top-level plugin, leave nil when nesting under an entity. They unmarshal cleanly too (the reverter's input).
- **`internal/aimap`** — the mapping tables **shared by both directions**: `EndpointTable`, `SectionFor`, `NormalizeCapability`, `PluginProvider`, the gemini/bedrock option-key sets, `LabelsToTags`/`TagsToLabels`, and the gateway-service constants. Edit mappings here so forward and reverse can never drift.
- **`convert`** — the lowering logic (AI Gateway → decK). `Convert` (bytes→bytes) wraps `ConvertDocument` (struct→struct). All state lives on the `Converter` struct: name-indexed registries (providers/policies/consumerGroups) built up front for cross-reference resolution, the output `*kong.Document`, and accumulated warnings.
- **`revert`** — the lifting logic (decK → AI Gateway). `Revert` wraps `RevertDocument`; state lives on `Reverter` (plugin/ai-models indexes, synthesized-provider and policy registries, warnings). Mirrors `convert`'s file layout and `warn`/strict semantics.
- **`cmd/ai-deck-converter`** — thin CLI: auto-detects direction (`_format_version` ⇒ `revert.Revert`, else `convert.Convert`), `-direction` overrides.

### Conversion flow

`Converter.run()` (in `convert.go`) executes a **fixed ordering**: registries → global policies → vaults → consumer groups → consumers → models → MCP servers → agents. Each step lives in its own file (`policy.go`, `vault.go`, `consumer.go`, `model.go`, `mcp.go`, `agent.go`).

Use `c.warn(...)` for unresolved references and unsupported features. In `-strict` mode `warn` returns an error (which callers must propagate); otherwise it appends to `c.warnings`. The converted document is still produced alongside warnings.

### The endpoint table is the heart of model conversion

`internal/aimap/endpoints.go` holds `EndpointTable[section][capability] → EndpointSpec`. `section` is chosen from the model's `llm_format` (`SectionFor`), **not** the provider type — the one exception is gemini-format traffic served by Vertex. The spec carries the path, methods, `route_type`, `genai_category`, regex flag, and whether the request body carries a `model` field.

`convertModels` (`convert/model.go`) is the most intricate code path:
- All model routes nest under **one shared `ai-gateway` Service** (placeholder url `http://ai-gateway.upstream.local`); `ai-proxy-advanced` overrides the real upstream per target.
- Models/targets that resolve to the **same** `(section, RouteLabel)` collapse into **one route** with multiple `ai-proxy-advanced` `targets[]` (the `routeGroup` accumulator, deduped by `target|route_type`).
- Body-model routes also get an `ai-model-selector` plugin; one `ai-models` entry is emitted per source model; model `policies`/`acls` become top-level plugins scoped via a `model:` FK.

When editing capability/endpoint behavior, edit `aimap.EndpointTable` and `aimap.NormalizeCapability` (handles loose spellings like `chat`→`generate` and fans `audio` out to speech/transcription/translation). The reference for these mappings is `ref/supported-endpoints.md`.

Provider auth and options are folded into each target by `convert/provider.go` (`resolveAuth`, `mapOptions`) — provider-specific keys get renamed/nested (e.g. under `model.options.gemini` / `.bedrock`).

### Reverse model reconstruction

`revert/model.go` + `revert/endpoints.go` + `revert/provider.go` invert the above:
- `resolveEndpoint` recovers `(capability, spec)` per target from progressively weaker signals: `route_type` within the section, the `{section}-{RouteLabel}` route name, `genai_category`, then the path shape (`basePathFor` also recovers the model's base path, including from regex routes).
- Targets group into one Model per `model_alias` (named by the matching `ai-models` entry; alias-less groups match alias-less `ai-models` entries by position, else fall back to the route name).
- Providers are **synthesized** from each target's auth/options (`defoldAuth`/`defoldOptions` reverse `resolveAuth`/`mapOptions`), deduped by fingerprint, named from the vault prefix (`openai-env`) or a per-type counter. The `gemini` plugin enum is disambiguated to vertex only by a vertex-style route path (`detectProviderType`).
- Forward defaults are dropped on the way back (e.g. `{algorithm: round-robin}` balancer) so round trips stay clean. `revert/roundtrip_test.go` asserts forward→reverse→forward is **byte-identical with zero warnings** for every `convert/testdata` case — keep it that way when changing either direction.

## Testing

Golden tests are the primary regression mechanism, in both directions. Each `convert/testdata/<case>/` (and `revert/testdata/<case>/`) has an `input.yaml`, an `expected.yaml`, and an optional `options.yaml` (overrides default `Options`). For revert cases, `input.yaml` is a decK config and `expected.yaml` is AI Gateway YAML; the `20_`/`21_` cases cover non-convention hand-written configs. To add a case: create the directory with `input.yaml`, run `go test ./<pkg> -run TestGolden -update`, then **review the generated `expected.yaml`** before committing — `-update` regenerates all cases, so inspect the diff. `revert/roundtrip_test.go` additionally re-converts every reverted forward golden and requires byte-identical output with zero warnings. `convert_test.go`, `provider_test.go`, `revert_test.go`, and the `internal/*` `_test.go` files cover units in isolation.

## Reference material

`ref/` contains the source-of-truth docs this converter encodes: `supported-endpoints.md`, the AI plugin docs (`ai-proxy-advanced.md`, `ai-mcp-proxy.md`, `ai-a2a-proxy.md`), admin API specs, and `ref/examples/models/<provider>/` pairs of AI-Gateway config + the hand-authored Kong decK output they should produce. Consult these when adding provider support or changing emitted plugin config. `examples/` holds end-to-end sample inputs.
