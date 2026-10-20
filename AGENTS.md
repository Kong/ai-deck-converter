# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go CLI/library that converts in **both directions** between a high-level **AI Gateway entity-model** YAML config and a **Kong Gateway decK** declarative config (`_format_version: "3.0"`). The AI Gateway model (models, model_providers, mcp_servers, agents, policies, consumers, vaults) is realized in Kong as Gateway entities plus the AI plugins (`ai-proxy-advanced`, `ai-model-selector`, `ai-mcp-proxy`, `ai-a2a-proxy`). The forward direction (`convert`) lowers AI Gateway → decK; the reverse (`revert`) lifts decK → AI Gateway, best-effort for hand-written configs. See `README.md` for the full entity-mapping table and known limitations.

## Commands

```sh
go build ./cmd/ai-deck-converter      # build the CLI binary
go test ./...                          # run all tests
go test ./convert -run 'TestGolden/01_single_model'   # run one forward golden case
go test ./convert -run TestGolden -update             # regenerate ALL forward golden expected.yaml
go test ./revert -run TestGolden -update              # regenerate ALL reverse golden expected.yaml
go test ./revert -run TestRoundTrip                   # forward→reverse→forward must be byte-identical
```

Run the tool: `ai-deck-converter -min-runtime-version 2.1 input.yaml` (stdout), `-o kong.yaml` to write a file, `-` for stdin. `-min-runtime-version` (`MAJOR.MINOR`) is required for to-deck/to-dbless and rejected for from-deck. The direction is auto-detected (`_format_version` present → decK→AI Gateway) and can be forced with `-direction to-deck|to-dbless|from-deck`. Flags: `-strict` (to-deck/to-dbless: exit 1 on any conversion issue, not only `ERROR` issues; from-deck: unresolved refs / unconvertible entities become errors), `-label-tag-prefix` (namespaces label-derived tags; stripped on revert).

## Architecture

One direction of dependency (`convert`/`revert` → `internal/*`):

- **`internal/aigw`** — the AI Gateway entity model. Structs that `aigw.Parse` unmarshals (and the reverter marshals — fields carry `omitempty`; `Balancer`/`ProviderRef`/`TargetModelConfig` have paired Unmarshal/MarshalYAML). One file per entity kind (`model.go`, `provider.go`, `mcp.go`, …). Credentials nest under consumers.
- **`internal/kong`** — the decK model. Structs (`deck.go`) shaped to marshal directly into the YAML decK expects: `_format_version`, nested routes/plugins, and name-based foreign keys (`Ref` → `{name: <x>}`). `Plugin` carries optional `Service`/`Route`/`Consumer`/`ConsumerGroup`/`Model` refs; set one to scope a top-level plugin, leave nil when nesting under an entity. They unmarshal cleanly too (the reverter's input).
- **`internal/aimap`** — the mapping tables **shared by both directions**: `EndpointTable`, `SectionFor`, `NormalizeCapability`, `PluginProvider`, the gemini/bedrock option-key sets, `LabelsToTags`/`TagsToLabels`, and the gateway-service constants. Edit mappings here so forward and reverse can never drift.
- **`convert`** — the lowering logic (AI Gateway → decK). `Convert` (bytes→`*ConversionResult`) is the only entry point; the result holds the output `*kong.Document`, its metadata, and every `ConversionIssue` (`convert/issue.go`). Callers encode the output with its `ToYAML` and `ToDBLess` methods. All state lives on the `Converter` struct: name-indexed registries (providers/policies/consumerGroups) built up front for cross-reference resolution, the output `*kong.Document`, and the collected issues.
- **`revert`** — the lifting logic (decK → AI Gateway). `Revert` wraps `RevertDocument`; state lives on `Reverter` (plugin/ai-models indexes, synthesized-provider and policy registries, warnings). Mirrors `convert`'s file layout. It keeps string warnings and the `Strict` option; RFC 00207 covers convert only.
- **`cmd/ai-deck-converter`** — thin CLI: auto-detects direction (`_format_version` ⇒ `revert.Revert`, else `convert.Convert`), `-direction` overrides.

### Conversion flow

`Converter.run()` (in `convert.go`) executes a **fixed ordering**: registries → global policies → vaults → consumer groups → consumers → models → MCP servers → agents → routing conflict check. Each step lives in its own file (`policy.go`, `vault.go`, `consumer.go`, `model.go`, `mcp.go`, `agent.go`, `lint.go`).

Routing conflicts follow `docs/decisions/00229-routing-conflict-detection.md`. `warnRoutingConflicts` compares each pair of routes of the output and gives a `ROUTING_CONFLICT` `WARNING` to each owner of the later route. Every model on a shared model route owns it (`c.modelRouteModels`). The model pass also warns when a model uses an alias that another model already uses, and warns when models on one route read the model name from different `ai-model-selector` sources. A disabled model (`enabled: false`) makes no output.

Version gates follow `docs/decisions/00225-dataplane-conversion-compatability.md`. `internal/aimap/versions.go` holds every gate (`Feature` → first release) and is the only place that compares versions. A convert step checks `c.supports(aimap.FeatureX)` and reports `UNSUPPORTED_IN_MIN_RUNTIME_VERSION` through `c.reportUnsupported`/`c.failUnsupported`. A new output that older data planes reject adds a `Feature` in the same change. Golden cases default to `aimap.LatestVersion`; set `min_runtime_version` in `options.yaml` to test below a gate (see the `8x_min_runtime_*` cases).

Issue reporting follows `docs/decisions/00207-conversion-issue-reporting.md`. Every issue carries a code, a severity, and a `ConversionIssueSource` (entity kind, name, and field paths relative to the entity, built with `issueSource(kind, name).at(fields...)`). Use `c.warn(src, code, ...)` for a `WARNING`, `c.reportError(src, code, ...)` for an `ERROR` that lets the entity convert, and `return c.fail(src, code, ...)` to leave out the entity: `fail` returns `errSkipEntity`. Each top-level entity converts inside `c.convertEntity`, which restores `c.out` when the entity fails and turns a panic or an unexpected error into `INTERNAL_ERROR`. Models share routes and plugins, so `convertModels` instead starts a new pass without the failed model (`convertModelsPass`, `modelPass`). Code on the model path must therefore never change the source document: `resolveEmbeddings` works on a `deepCopy`. A nested item that cannot be converted (an MCP tool, a model target, a consumer credential) is left out with a `WARNING` whose path points at the item; an unknown or unconvertible auth strategy always fails the entity.

### The endpoint table is the heart of model conversion

`internal/aimap/endpoints.go` holds `EndpointTable[section][capability] → EndpointEntry{Primary EndpointSpec; Secondary []EndpointSpec}`. Secondary is only set for capabilities reachable through more than one endpoint: bedrock `generate` (also served by `/invoke`), and the gemini section, which lists each capability's Gemini Enterprise endpoint as a secondary (the Gemini Enterprise-only image/video/rerank have Gemini Enterprise paths as primary). Read entries through `aimap.EndpointsFor(section, capability)` for one capability or `aimap.SectionEndpoints(section)` to scan a whole section — never index `EndpointTable`'s fields directly, or forward/reverse can drift. `section` is chosen from the model's `llm_format` (`SectionFor`), **not** the provider type; there is no separate vertex section (`vertex` is only accepted as a format alias of `gemini`). Each spec carries the path, methods, `route_type`, `genai_category`, regex flag, and whether the request body carries a `model` field.

`convertModels` (`convert/model.go`) is the most intricate code path:
- All model routes nest under **one shared `ai-gateway` Service** (placeholder url `http://ai-gateway.upstream.local`); `ai-proxy-advanced` overrides the real upstream per target. The exception is WebSocket endpoints (`aimap.IsWebSocketEndpoint`, i.e. `realtime`): they nest under `ai-gateway-websocket` (`ws://…`), get one route per model with `ws`/`wss` and no methods, no `ai-model-selector`, and route-scoped plugins carrying the route's `protocols`. Targets there are alias-less: no selector presents an alias, so aliased targets fail with "failed to get balancer instance" (proven by `e2e/` case `realtime_model_websocket_transport`). The `aimap.EncodeRealtimeModel` tag on the plugin links it back to its `ai_models` entry for revert. Policy plugins not in `aimap.SupportsWebSocket` (all AI policy plugins) are skipped on the route with a warning, because the data plane rejects the whole config otherwise. A global authentication policy fails every WebSocket model for the same reason. Auth plugins there drop the `anonymous` fallback, since `request-termination` does not run on ws/wss. See `convert/testdata/70_realtime_websocket_transport`.
- A capability with secondary endpoints emits **one route per endpoint** (bedrock `generate` → `bedrock-converse` + `bedrock-invoke`), each carrying the same target(s). Bedrock's invoke spec for `generate` is reused verbatim from `audio/speech` (same endpoint), so a model declaring both capabilities gets one `bedrock-invoke` route/target, not two — the target-fingerprint dedup below collapses them. See `convert/testdata/57_bedrock_generate_invoke` and `58_bedrock_generate_and_speech`.
- Models/targets that resolve to the **same** `(section, RouteLabel)` collapse into **one route** with multiple `ai-proxy-advanced` `targets[]` (the `routeGroup` accumulator, deduped by `target|route_type`).
- Body-model routes also get an `ai-model-selector` plugin; one `ai-models` entry is emitted per source model (per `config.route.model.values` entry for multi-alias models, each alias getting its own `ai-proxy-advanced`/policy copy, linked for revert by the `aimap.EncodeModelAliasGroup` tag); model `policies`/`acls` become top-level plugins scoped via a `model:` FK.
- A semantic-balancer model's `config.balancer.vectordb` is lowered via `aimap.VectorDBToPlugin`, then a `datastores` reference (if any) is substituted into it by `resolveModelDatastore`/`modelVectorDB` — see "Datastore support" below.

When editing capability/endpoint behavior, edit `aimap.EndpointTable` and `aimap.NormalizeCapability` (handles loose spellings like `chat`→`generate` and fans `audio` out to speech/transcription/translation). The reference for these mappings is `ref/supported-endpoints.md`.

Provider auth and options are folded into each target by `convert/provider.go` (`resolveAuth`, `mapOptions`) — provider-specific keys get renamed/nested (e.g. under `model.options.gemini` / `.bedrock`).

### Reverse model reconstruction

`revert/model.go` + `revert/endpoints.go` + `revert/provider.go` invert the above:
- `resolveEndpoint` recovers `(capability, spec)` per target from progressively weaker signals: `route_type` within the section (scanned via `aimap.SectionEndpoints`, so secondary specs are candidates too), the `{section}-{RouteLabel}` route name, `genai_category`, then the path shape (`basePathFor` also recovers the model's base path, including from regex routes). A route/target with no other distinguishing signal resolves to whichever candidate capability sorts first alphabetically — for a bedrock `bedrock-invoke` route this means `audio/speech` beats `generate`'s secondary spec (they're field-for-field identical), a caveat that never breaks round-tripping a converter-produced config since re-converting the recovered model reproduces the same route/target either way. `revert/model.go`'s per-model capability set is a union across all of that model's routes, so a model's `bedrock-converse` route still contributes `generate` even when its `bedrock-invoke` route resolves as `audio/speech`.
- Targets group into one Model per `model_alias` (named by the matching `ai-models` entry; alias-less groups match alias-less `ai-models` entries by position, else fall back to the route name).
- Providers are **synthesized** from each target's auth/options (`defoldAuth`/`defoldOptions` reverse `resolveAuth`/`mapOptions`), deduped by fingerprint, named from the vault prefix (`openai-env`) or a per-type counter. The `gemini` plugin enum is disambiguated to vertex only by a Gemini Enterprise-style route path whose target is not also served on a Gemini-style path (`detectProviderType`, `indexGeminiPathTargets`). Alias-less targets on a secondary endpoint's route fold into the model group of the primary route (`aliaslessGroupServing`).
- Forward defaults are dropped on the way back (e.g. `{algorithm: round-robin}` balancer) so round trips stay clean. `revert/roundtrip_test.go` asserts forward→reverse→forward is **byte-identical with zero revert warnings and zero convert issues** for every `convert/testdata` case, except the `ROUTING_CONFLICT` warnings that the case's `issues.yaml` already has — keep it that way when changing either direction.

### Datastore support (policies and models)

A `Datastore` (`internal/aigw/datastore.go`) is a shared connection (`redis-ce`/`redis-ee`/`vectordb`) declared at the top level of the document and referenced by name — `datastores: [{name: x}]` — from a Policy or from a semantic-balancer Model. `internal/aimap/datastore.go` owns every rule about the pairing, shared by both callers: `ApplyDatastore` (policies) checks plugin-type support and cardinality, then substitutes the connection at a plugin-specific `ConfigPath`; `ApplyModelDatastore` (models) restricts to `redis-ee`/`vectordb` and substitutes into `config.vectordb`'s strategy sub-block — always *after* `aimap.VectorDBToPlugin` has lowered that block, since a Datastore's `config` is already plugin-shaped. `convert/policy.go`'s `applyDatastore` and `convert/model.go`'s `resolveModelDatastore`/`modelVectorDB` are the only callers, and they alone decide how to report a dangling reference (aimap returns `*aimap.UnknownDatastoreError`). Both entity kinds behave identically: an unknown name gives an `UNDEFINED_REFERENCE` error, and the entity converts without the connection. Every other invalid reference — more than one Datastore, a non-semantic balancer, a disallowed type — leaves out the model, or leaves out the policy with an error while its referencing entities still convert. `type: api` models sharing one route-scoped `ai-proxy-advanced` (see `convertModels` above) cannot carry conflicting datastores — `convertModels` leaves out the later model rather than silently dropping one model's datastore or having another model inherit it. Datastores are convert-only: `revert/` inlines the substituted connection straight into the entity's `config` and never reconstructs a Datastore or its reference, which is why `revert/roundtrip_test.go` still passes byte-identical for datastore-bearing golden cases.

## Testing

Golden tests are the primary regression mechanism, in both directions. Each `convert/testdata/<case>/` (and `revert/testdata/<case>/`) has an `input.yaml`, an `expected.yaml`, an `issues.yaml` for forward cases that report issues, and an optional `options.yaml` (overrides default `Options`; the test-only key `output_mode: db-less` makes a forward case compare `ToDBLess()` output). For revert cases, `input.yaml` is a decK config and `expected.yaml` is AI Gateway YAML; the `20_`/`21_` cases cover non-convention hand-written configs. To add a case: create the directory with `input.yaml`, run `go test ./<pkg> -run TestGolden -update`, then **review the generated `expected.yaml`** before committing — `-update` regenerates all cases, so inspect the diff. `revert/roundtrip_test.go` additionally re-converts every reverted forward golden and requires byte-identical output with zero issues. The `75_`–`79_issue_*` forward cases cover the RFC 00207 partial-output rules. `92_routing_conflicts` covers RFC 00229. `convert_test.go`, `provider_test.go`, `revert_test.go`, and the `internal/*` `_test.go` files cover units in isolation.

## Code Comment Style (Mandatory)

This rule applies to **every** comment you write or touch: Go doc comments, inline
comments, test comments, commit messages, and code blocks in Markdown. It applies
to new comments and to comments you edit for any other reason.

Write comments in **ASD-STE100 Simplified Technical English**.

### Comment Only When Needed

The default is **no comment**. Write a comment only if it gives the reader a fact
that the code cannot show. Valid reasons:

- The code does something unexpected. The comment tells why.
- A constraint, invariant, or side effect is not visible in the code.
- The code is a workaround for an external bug. Include the issue link.
- Go requires a doc comment on an exported identifier.

Do not write a comment that:

- Tells what the code does. The code already shows this.
- Describes your change, for example `added X`, `now uses Y`, `fixed bug`.
  Put this in the commit message.
- Labels an obvious block, for example `// Loop over servers`.
- Tells the steps of the function one by one.

If a better name or a smaller function removes the need for a comment, change the
code. Do not write the comment.

When a comment is necessary, keep only the essential fact. One sentence is the
target. Use more only when the reader needs each sentence.

Do not delete existing comments that you do not touch. This section applies to
comments you write or edit. Commit messages are always necessary.

### Hard Rules

1. **One idea per sentence.** Split any sentence that has two clauses joined by
   `and`, `but`, `so`, `because`, `which`, `where`, or a colon.
2. **Maximum 20 words per sentence.** Count them. If over, split.
3. **One sentence is the target. Maximum 3 sentences per comment block.** If you
   need more, the code needs a better name or a smaller function, not a longer
   comment.
4. **Present tense, active voice.** Write `X changes Y`, not `X is used to
   transform Y` or `Y gets transformed by X`.
5. **One meaning per word.** Use the simplest verb that is correct:
- `change` (not transform, translate, convert, marshal into, coerce)
- `map` only when the code literally maps keys to values
- `get` (not retrieve, fetch, obtain, acquire)
- `send` (not dispatch, propagate, emit, forward on)
- `make` (not construct, instantiate, materialize)
- `use` (not leverage, utilize, employ)
- `start` / `stop` (not initiate, terminate, tear down)
6. **No parenthetical asides and no em dashes.** If the aside matters, make it its
   own sentence. If it does not matter, delete it.
7. **No filler.** Ban list: `simply`, `basically`, `essentially`, `note that`,
   `it is worth noting`, `of course`, `obviously`, `in order to`, `please be aware`.
8. **No hedging narrative.** Do not write `so that an update that leaves it out
   does not silently clear it`. Write the fact: `the stored value stays`.
9. **Do not restate the code.** If a sentence repeats the code or the function
   signature in prose, delete it. If no sentence stays, delete the comment.
10. **Go doc comments start with the identifier name.** `// FooBar does X.`
11. **Wrap at 80 columns.** Never break a line mid-sentence in a way that leaves a
    word glued to the next comment marker.
12. **No emoji, no exclamation marks.**

## Design Decisions (RFCs)

- Substantive design decisions are recorded as RFCs under `docs/decisions/`
  Start from `docs/decisions/00000-template.md`.