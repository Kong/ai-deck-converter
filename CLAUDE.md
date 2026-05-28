# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go CLI/library that lowers a high-level **AI Gateway entity-model** YAML config into a **Kong Gateway decK** declarative config (`_format_version: "3.0"`). The AI Gateway model (models, providers, mcp_servers, agents, policies, consumers, vaults) is realized in Kong as Gateway entities plus the AI plugins (`ai-proxy-advanced`, `ai-model-selector`, `ai-mcp-proxy`, `ai-a2a-proxy`). See `README.md` for the full entity-mapping table and known limitations.

## Commands

```sh
go build ./cmd/ai-deck-converter      # build the CLI binary
go test ./...                          # run all tests
go test ./convert -run 'TestGolden/01_single_model'   # run one golden case
go test ./convert -run TestGolden -update             # regenerate ALL golden expected.yaml
```

Run the tool: `ai-deck-converter input.yaml` (stdout), `-o kong.yaml` to write a file, `-` for stdin. Flags: `-strict` (unresolved refs become errors), `-label-tag-prefix` (namespaces label-derived tags).

## Architecture

Three layers, one direction of dependency (`convert` → `internal/*`):

- **`internal/aigw`** — the **input** model. Structs that `aigw.Parse` unmarshals the source YAML into. One file per entity kind (`model.go`, `provider.go`, `mcp.go`, …). Credentials nest under consumers.
- **`internal/kong`** — the **output** model. decK structs (`deck.go`) shaped to marshal directly into the YAML decK expects: `_format_version`, nested routes/plugins, and name-based foreign keys (`Ref` → `{name: <x>}`). `Plugin` carries optional `Service`/`Route`/`Consumer`/`ConsumerGroup`/`Model` refs; set one to scope a top-level plugin, leave nil when nesting under an entity.
- **`convert`** — the lowering logic. `Convert` (bytes→bytes) wraps `ConvertDocument` (struct→struct). All state lives on the `Converter` struct: name-indexed registries (providers/policies/consumerGroups) built up front for cross-reference resolution, the output `*kong.Document`, and accumulated warnings.
- **`cmd/ai-deck-converter`** — thin CLI wrapper over `convert.Convert`.

### Conversion flow

`Converter.run()` (in `convert.go`) executes a **fixed ordering**: registries → global policies → vaults → consumer groups → consumers → models → MCP servers → agents. Each step lives in its own file (`policy.go`, `vault.go`, `consumer.go`, `model.go`, `mcp.go`, `agent.go`).

Use `c.warn(...)` for unresolved references and unsupported features. In `-strict` mode `warn` returns an error (which callers must propagate); otherwise it appends to `c.warnings`. The converted document is still produced alongside warnings.

### The endpoint table is the heart of model conversion

`convert/endpoints.go` holds `endpointTable[section][capability] → endpointSpec`. `section` is chosen from the model's `llm_format` (`sectionFor`), **not** the provider type — the one exception is gemini-format traffic served by Vertex. The spec carries the path, methods, `route_type`, `genai_category`, regex flag, and whether the request body carries a `model` field.

`convertModels` (`convert/model.go`) is the most intricate code path:
- All model routes nest under **one shared `ai-gateway` Service** (placeholder url `http://ai-gateway.upstream.local`); `ai-proxy-advanced` overrides the real upstream per target.
- Models/targets that resolve to the **same** `(section, RouteLabel)` collapse into **one route** with multiple `ai-proxy-advanced` `targets[]` (the `routeGroup` accumulator, deduped by `target|route_type`).
- Body-model routes also get an `ai-model-selector` plugin; one `ai-models` entry is emitted per source model; model `policies`/`acls` become top-level plugins scoped via a `model:` FK.

When editing capability/endpoint behavior, edit `endpointTable` and `normalizeCapability` (handles loose spellings like `chat`→`generate` and fans `audio` out to speech/transcription/translation). The reference for these mappings is `ref/supported-endpoints.md`.

Provider auth and options are folded into each target by `convert/provider.go` (`resolveAuth`, `mapOptions`) — provider-specific keys get renamed/nested (e.g. under `model.options.gemini` / `.bedrock`).

## Testing

Golden tests in `convert/golden_test.go` are the primary regression mechanism. Each `convert/testdata/<case>/` has an `input.yaml`, an `expected.yaml`, and an optional `options.yaml` (overrides default `Options`). To add a case: create the directory with `input.yaml`, run `go test ./convert -run TestGolden -update`, then **review the generated `expected.yaml`** before committing — `-update` regenerates all cases, so inspect the diff. `convert_test.go`, `provider_test.go`, and the `internal/*` `_test.go` files cover units in isolation.

## Reference material

`ref/` contains the source-of-truth docs this converter encodes: `supported-endpoints.md`, the AI plugin docs (`ai-proxy-advanced.md`, `ai-mcp-proxy.md`, `ai-a2a-proxy.md`), admin API specs, and `ref/examples/models/<provider>/` pairs of AI-Gateway config + the hand-authored Kong decK output they should produce. Consult these when adding provider support or changing emitted plugin config. `examples/` holds end-to-end sample inputs.
