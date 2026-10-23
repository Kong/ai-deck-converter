---
description: Support setting a model's capabilities and formats list to all
approvers:
  - team-koko
tags:
  - convert
  - models
  - api
jira: https://konghq.atlassian.net/browse/KOKO-4510
---

# Motivation
[motivation]: #motivation

Today the user must write each capability and format of a model. A release
that adds one needs a change of these lists. Users ask for a `*` value that
selects all endpoints that the targets of the model support.

# Scope
[scope]: #scope

In scope:
- setting `*` in model `capabilities`
- setting `*` in model `formats`
- support creating models with more than one format
- the format rules that the data plane applies to each provider

Out of scope:
- including the `passthrough` format as part of `*`

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

`*` will serve as a stand-in to represent selecting all formats and
capabilities.

To achieve the desired behavior the AI deck converter will expand `*` into
the list of supported `formats`/`capabilities` by iterating over each
model, inspecting its targets, and building the list of supported
endpoints. Then the converter will create the supported endpoints for each
model.

Note: the data plane will reject configuration that contains endpoints that are
not supported by the targets of the model.

## Example

In the below example, each route balances only over the targets that
support its endpoint. As a result, failover can differ between routes.

```yaml
models:
  - name: a                     # every capability except batches and files
    capabilities: ["*"]
    formats: [{type: "*"}]
    targets:
      - name: primary
        provider: openai-prod   # supports skills
      - name: fallback
        provider: azure-prod    # does not support skills
  - name: b
    type: api                   # only batches and files
    capabilities: ["*"]
    formats: [{type: cohere}, {type: openai}]
    targets:
      - name: primary
        provider: openai-prod
```

- Model `a` gets the `openai` endpoints and the `anthropic` `messages`
  endpoint, with no issue. Its `skills` route has only `primary`, so it
  cannot fail over to `fallback`.
- Model `b` gets the `openai` `batches` and `files` endpoints. No target
  supports the explicit `cohere` format, so it gets a `CONFIG_FIELD_DROPPED`
  warning.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

## Parsing lists containing `*`

- `*` is a new value of `capabilities` and of `Format.Type`.
- A list with `*` ignores its other values.
  - `capabilities` set to `["*", generate]` is equivalent to `["*"]`.
  - `formats` set to `[{type: "*"}, {type: passthrough}]` is equivalent to
    `[{type: "*"}]`.
- An absent or empty list keeps its meaning of today.
  - `formats` set to `[]` is equivalent to `[{type: openai}]`.
  - `capabilities` set to `[]` selects no capability.
- Each format must be a value of `aimap.Formats()` or `*`.
  - Any other value fails the model. Today it fails only when no endpoint
    serves its capabilities.

## Expansion of `*` into model formats and target capabilities

```go
// Wildcard is the formats and capabilities value that selects every value.
const Wildcard = "*"

// SupportedFormats returns the formats represented by "*" for a given
// list of model targets and minRuntimeVersion.
func SupportedFormats(targets []Target, minRuntimeVersion Version) []aigw.Format

// SupportedCapabilities returns the capabilities represented by "*" for a given
// format, target, model type, and minimum runtime version.
func SupportedCapabilities(
	format aigw.Format, target Target, modelType string, minRuntimeVersion Version,
) []aigw.Capability
```

The ai-deck-converter will convert `*` into the formats and capabilities
supported by each model via new `SupportedFormats()` and
`SupportedCapabilities()` methods. Details for these methods are provided
in [Implementation Notes](#implementation-notes).

Note: `SupportedFormats()` and `SupportedCapabilities()` validate their
inputs against two things: the AI data plane's validation rules, and the
feature gates for the minimum runtime version. The feature gates keep
version-specific capabilities and formats from leaking into runtimes that
don't support them.

## Support for multiple model formats

Today the converter only supports a single model format. With this RFC,
the converter will need to support models having multiple formats.

To support multiple formats for a model, the following updates will be
needed:
- add a loop over the model's formats around the existing target and
  capability loop in `convertModel`
  - for each format, work out its section and `llm_format`, then run the
    existing target and capability logic for that format
- order format/capability combinations to support round-trip stability
  - the order should be `openai` first and then the remaining formats
    sorted, and within each format `generate` first then the rest sorted
- handle the case when one format can't serve a capability but another
  format can
  - this should be handled by skipping the combination that can't serve
    the capability
  - the model fails only when no format can serve the capability
- handle the case when multiple format/capability combinations share the
  same path
  - this should be handled by choosing the first format in the set order
    that still has at least one target using it
  - any target that loses out but had explicitly asked for the capability
    should get a warning
- update the video lifecycle logic to check whether `openai` is anywhere
  in the model's list of formats

# Drawbacks
[drawbacks]: #drawbacks

- **Some configurations that load today convert differently**: a second
  format adds routes, an unknown second format fails the model, and the
  new pair order can reorder routes and plugins.
- **Many generated routes**: one `type: model` model on an `openai`
  provider makes 12 routes with `*` in both lists.
- **Requests can fail on a route that loads**, because `TargetSupports`
  checks the provider and not the model.
- **Some results under `*` give no issue**, for example, a failover that
  differs for each endpoint.
- **Output changes with the converter release**, because each model with
  `*` gets every endpoint that a new release adds.
- **More routing conflicts**, because a `*` model uses every supported path
  under its base path.
- **`type: api` models on one route stay merged after a revert**, which `*`
  makes more frequent.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

`*` must only select the endpoints that the targets of the model support,
because the data plane rejects every other endpoint.

Alternatives:
- **Select every endpoint of every format**, which the data plane rejects.
- **Apply `TargetSupports` only under `*`**, which breaks the round trip.
- **Select only the endpoints that all targets support**, which lets one
  limited target remove endpoints from the whole model.
- **Default absent or empty lists to every value**, which adds routes to
  every existing model.
- **A boolean field such as `all_capabilities: true`**, which uses two
  fields for one list.
- **Warn for each value that `*` leaves out or changes**, which stops
  `-strict` callers from using `*`.
- **Never warn for a target that cannot serve a capability**, which hides
  failover differences in explicit lists.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

None.

# Implementation notes
[implementation-notes]: #implementation-notes

## Target support

The data plane rejects the whole configuration when a target breaks one
of its validation rules. The rules live in `kong/llm/schemas/init.lua`,
`kong/llm/schemas/embeddings.lua`, and the `ai-proxy` and
`ai-proxy-advanced` plugin schemas. `internal/aimap` copies the rules that
depend on the endpoint for both directions:

```go
// Target holds the target facts that the data plane checks.
type Target struct {
	ProviderType string
	// Mantle is true when a bedrock target sets endpoint_type to mantle.
	Mantle bool
	// BatchBucketPrefix is true when a bedrock target sets
	// batch_bucket_prefix.
	BatchBucketPrefix bool
	// Llama2RawFormat is true when a llama2 target sets format to raw.
	Llama2RawFormat bool
}

// TargetSupports reports whether the data plane accepts target on spec in
// format.
func TargetSupports(format string, target Target, spec EndpointSpec) bool
```

`TargetSupports` is true when all seven conditions are true:

1. The format accepts the provider. This is true in these cases:
    - The format is `openai`.
    - The format equals the provider type.
    - The format is `anthropic`, and the route type is `llm/v1/chat`.
2. The route type accepts the provider. See [Route types](#route-types).
3. A `gemini` target on `realtime/v1/realtime` uses the `gemini` format.
   See [Support for multiple model formats](#support-for-multiple-model-formats).
4. A `mantle` target uses the `openai` or `anthropic` format. Its route
   type is in the mantle list. The mantle list replaces condition 2 for a
   mantle target. See [Bedrock mantle](#bedrock-mantle).
5. A `bedrock` target on `llm/v1/batches` sets `batch_bucket_prefix`.
6. An `openai` or `anthropic` target on `llm/v1/skills` uses its own
   format. The existing `aimap.RequiresNativeFormat` check moves here.
7. A `llama2` target with `format: raw` or `llama2_format: raw` is not on
   `llm/v1/embeddings`. The converter sends both keys as `llama2_format`.

`TargetSupports` applies to `*` and to explicit lists.

These rules need no condition:

- `azure` with an Anthropic upstream rejects `llm/v1/completions`, but no
  endpoint uses `llm/v1/completions`.
- The `passthrough` rules stay in the existing checks, because `*` never
  selects `passthrough`.
- Provider-only rules, for example `gemini` with
  `auth.allow_override: false`, do not depend on the endpoint.

### Route types

The table copies `SUPPORTED_ROUTE_TYPES` of `kong/llm/schemas/init.lua` for
2.3, keyed by the plugin provider of the target:

| Route type                      | Plugin providers                                                                                                                                                                                                                    |
|---------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `llm/v1/chat`                   | `openai`, `azure`, `anthropic`, `cohere`, `mistral`, `llama2`, `gemini`, `bedrock`, `databricks`, `huggingface`, `dashscope`, `xai`, `cerebras`, `vllm`, `deepseek`, `ollama`, `kimi`, `vercel`, `sagemaker`, `typesafe`, `baseten` |
| `llm/v1/completions`            | `openai`, `azure`, `anthropic`, `cohere`, `mistral`, `llama2`, `gemini`, `bedrock`, `huggingface`, `xai`, `ollama`                                                                                                                  |
| `llm/v1/embeddings`             | `openai`, `azure`, `cohere`, `mistral`, `llama2`, `gemini`, `bedrock`, `databricks`, `huggingface`, `dashscope`, `ollama`, `vercel`                                                                                                 |
| `llm/v1/responses`              | `openai`, `azure`, `xai`, `vercel`                                                                                                                                                                                                  |
| `llm/v1/assistants`             | `openai`, `azure`                                                                                                                                                                                                                   |
| `llm/v1/batches`                | `openai`, `azure`, `bedrock`, `anthropic`, `gemini`, `kimi`                                                                                                                                                                         |
| `llm/v1/files`                  | `openai`, `azure`, `kimi`                                                                                                                                                                                                           |
| `llm/v1/skills`                 | `openai`, `anthropic`                                                                                                                                                                                                               |
| `image/v1/images/generations`   | `openai`, `azure`, `bedrock`, `gemini`, `dashscope`, `xai`, `vercel`                                                                                                                                                                |
| `image/v1/images/edits`         | `openai`, `azure`, `bedrock`, `gemini`, `dashscope`                                                                                                                                                                                 |
| `audio/v1/audio/speech`         | `openai`, `azure`                                                                                                                                                                                                                   |
| `audio/v1/audio/translations`   | `openai`, `azure`                                                                                                                                                                                                                   |
| `audio/v1/audio/transcriptions` | `openai`, `azure`                                                                                                                                                                                                                   |
| `realtime/v1/realtime`          | `openai`, `azure`, `gemini`                                                                                                                                                                                                         |
| `video/v1/videos/generations`   | `openai`, `azure`, `bedrock`, `gemini`, `huggingface`                                                                                                                                                                               |

Earlier releases differ only by `sagemaker` (2.1), `typesafe` (2.2),
`llm/v1/skills` (2.2), and `baseten` (2.3), which existing feature gates
cover.

### Statistics

The table copies the union of `UNSUPPORTED_LOG_STATISTICS` in 2.0 to 2.3:

| Route type                      | Plugin providers      | Releases   |
|---------------------------------|-----------------------|------------|
| `llm/v1/completions`            | `anthropic`           | 2.0 to 2.3 |
| `llm/v1/assistants`             | `openai`, `azure`     | 2.0 to 2.3 |
| `llm/v1/batches`                | `openai`, `azure`     | 2.0 to 2.3 |
| `llm/v1/skills`                 | `openai`, `anthropic` | 2.2 to 2.3 |
| `image/v1/images/generations`   | `bedrock`             | 2.0 to 2.3 |
| `image/v1/images/edits`         | `bedrock`             | 2.0 to 2.3 |
| `audio/v1/audio/speech`         | `openai`, `azure`     | 2.0 to 2.3 |
| `audio/v1/audio/transcriptions` | `openai`, `azure`     | 2.0        |
| `audio/v1/audio/translations`   | `openai`, `azure`     | 2.0 to 2.3 |

```go
// LogStatisticsSupported reports whether the data plane accepts
// log_statistics on a target of providerType on spec.
func LogStatisticsSupported(providerType string, spec EndpointSpec) bool
```

- When it is false, the converter sets `log_statistics: false` on the
  target.
- Today it changes only a `bedrock` target on the `openai` images route.
- A model with `config.logging.statistics: true` gets a
  `CONFIG_FIELD_OVERRIDDEN` warning, also under `*`.

### Bedrock mantle

- A mantle target serves only `llm/v1/chat`, `llm/v1/responses`,
  `llm/v1/files`, and `passthrough` in the `openai` or `anthropic` format.
- This list replaces the `bedrock` row of the route type table. The
  `bedrock` row has no `llm/v1/responses` or `llm/v1/files`, but a mantle
  target serves both.
- Condition 4 moves the existing `aimap.MantleSupports` check into
  `TargetSupports`.

### Required options

- Condition 5 leaves out `batches` for a `bedrock` target without
  `batch_bucket_prefix`.
- Other required options depend only on the provider or on `passthrough`,
  so `*` does not change them.
  - `mistral_format` and `anthropic_version` are not required for
    `passthrough`.
  - A `databricks` target needs `upstream_url` for `passthrough` and
    `workspace_instance_id` otherwise.

### Model name header

- The data plane forces `model_name_header: false` on `llm/v1/assistants`,
  `llm/v1/files`, `llm/v1/batches`, and `llm/v1/skills`.
- `aimap.SupportsModelNameHeader` moves this existing list out of
  `convert/model.go`.

### Realtime payloads

- The converter sets `log_payloads: false` on each `realtime/generation`
  target, because the data plane rejects `true`.
- A model with `config.logging.payloads: true` gets a
  `CONFIG_FIELD_OVERRIDDEN` warning, also under `*`.

## SupportedFormats and SupportedCapabilities

`SupportedFormats` returns `aimap.Formats()` without:

- `passthrough`,
- formats that a `FormatFeature` gate leaves out,
- formats in which no target has an endpoint that `TargetSupports`
  accepts.

`SupportedCapabilities` returns `aimap.CapabilitiesFor(format.Type,
target.ProviderType)` without:

- capabilities with no endpoint that `TargetSupports` accepts,
- capabilities that the model type does not allow (`type: api` gets only
  `batches` and `files`),
- capabilities that a `CapabilityFeature` gate leaves out,
- capabilities whose endpoint an earlier capability of another genai
  category already uses.

A format whose capabilities are all left out makes no routes and gives no
issue. The converter removes duplicate formats and uses the pair order of
[Support for multiple model formats](#support-for-multiple-model-formats).

## Special cases

- **`realtime`**: under `*`, a model that cannot protect a WebSocket route
  loses only its `realtime` endpoint.
- **Video lifecycle routes**: a `video` capability from `*` gives no
  lifecycle warning.
- **Version gates**: `*` adds no `Feature`, because it makes only routes
  and plugins that exist today.

## Issues

- **No issue when `*` causes the result.**
- **An issue when the model or a target cannot serve an explicit value.**
- A dropped pair or endpoint gives no issue while another pair or
  endpoint still serves the capability.

| Case                                                        | Under `*`       | Explicit list                                                                       |
|-------------------------------------------------------------|-----------------|-------------------------------------------------------------------------------------|
| `type` leaves out a capability                              | left out        | not changed                                                                         |
| Format that gives no endpoint for any target                | left out        | `WARNING` `CONFIG_FIELD_DROPPED` on `formats[i].type`                               |
| Only endpoint shares a route with another genai category    | left out        | `WARNING` `CONFIG_FIELD_DROPPED` on the capability                                  |
| Gated format or capability                                  | left out        | model fails, `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`                                   |
| Target cannot serve a capability that another target serves | target left out | target left out, `WARNING` `BEHAVIOR_MAY_DIFFER` on `capabilities[i]`, `targets[j]` |
| Target loses an equal path to another format                | target left out | target left out, `WARNING` `BEHAVIOR_MAY_DIFFER` on `capabilities[i]`, `targets[j]` |
| Video lifecycle route with 2+ targets                       | no issue        | `WARNING` `BEHAVIOR_MAY_DIFFER` on `targets`                                        |
| Two video models on one route                               | no issue        | `WARNING` `ROUTING_CONFLICT` on `config.route`                                      |
| `realtime` that the model cannot protect                    | left out        | model fails, `CONFIG_FIELD_INVALID`                                                 |
| No target serves a capability                               | N/A             | model fails, `CONFIG_FIELD_INVALID` on the capability                               |
| Only gated formats serve a capability                       | N/A             | model fails, `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`                                   |
| A value next to `*`                                         | ignored         | N/A                                                                                 |

An unknown format in an explicit list fails the model with
`CONFIG_FIELD_INVALID` on `formats[i].type`.

## Reverse conversion

The reverter never writes `*`; it writes the union of all targets'
formats and capabilities in pair order, with three changes:

- **Tied endpoints**: it leaves out a capability that only tied routes
  give, such as `audio/speech` on bedrock `invoke`, when another route
  gives a capability of the same tie.
- **Forced values**: it reads `log_statistics`, `log_payloads`, and
  `model_name_header` only from endpoints that support them, which changes
  `60_skills_api`.
  - A value that no endpoint of the model supports stays unset.
  - A `log_statistics` value equal to the default also stays unset when
    an endpoint of the model forces `false`. Set explicitly, it would give
    a `CONFIG_FIELD_OVERRIDDEN` warning on the second conversion.
- **Alias-less groups**: it groups alias-less routes by sorted target
  fingerprints and base paths, so one `type: api` model reverts to one
  model, a fix that can ship separately.
  - It uses this key only when the group count equals the count of free
    `ai_models` entries.
  - A free entry has no alias and no aliased model group uses it.

The round trip stays byte-identical. For a forward model with
`capabilities: ["*"]`, it accepts the `BEHAVIOR_MAY_DIFFER` and
`ROUTING_CONFLICT` warnings of the Issues table.

## Code changes

- `expandCapabilities` becomes `targetPairs` in
  `convert/model_endpoints.go`, which returns the ordered pairs of one
  target that `TargetSupports` accepts.
- The format loop of `convertModel` runs over those pairs, and the
  equal-path decision runs after all targets.
- A pair with no endpoint is skipped, and the model fails only when no
  pair serves the capability.
- `checkModelCapabilities` gets only explicit capabilities, and
  `llmFormat` takes the format of the current pair.
- `videoLifecycleCandidates` checks whether `openai` is in the effective
  format list.
- `targetSupports` becomes `aimap.TargetSupports`, and the `checkTarget`
  mantle check stays only for models with no `openai` or `anthropic`
  format.
- Under `*`, `targetPairs` leaves out a WebSocket endpoint that a global
  auth policy or more than one auth strategy cannot protect. An explicit
  `realtime` keeps the existing checks, which fail the model.
- An `openai` or `anthropic` target on `llm/v1/skills` in another format
  now gives "no target can serve capability" instead of the old
  "passthrough-only" error.
- `buildTarget` and the reverter call `aimap.LogStatisticsSupported`
  instead of reading `spec.SupportsLogStatistics`.
- `resolveEndpoint` returns tied capabilities, and
  `aimap.SupportsModelNameHeader` replaces `supportsModelNameHeader`.
- `convert/lint.go` and `CLAUDE.md` update their comments for `*` and the
  new round-trip warnings.

# Testing
[testing]: #testing

## Unit tests

Unit tests in `internal/aimap` cover:

- `TargetSupports`, with one case for each of its seven conditions.
- `LogStatisticsSupported`, with one case for each statistics table entry.
- `SupportedFormats` and `SupportedCapabilities`, with the model type,
  feature gate, and genai category rules.
- Each spec of a native section accepts its native provider.

## Forward golden cases

New cases in `convert/testdata`:

- `87_min_runtime_wildcard_skips_gated`: a `*` model at 2.1 with no issue.
- `91_wildcard_all`: each route of a `*` model has only the targets that
  support it.
- `98_wildcard_issues`: an unknown format and a misspelled capability.
- `99_multi_format_order`: pair order, duplicate formats, and
  `(openai, skills)` on `anthropic`.
- `100_wildcard_realtime_global_auth`: `realtime` is left out with no
  issue.
- `101_wildcard_targets_and_video`: a `*` model with two targets and video
  models on one route.
- `102_wildcard_realtime_auth_strategies`: `realtime` stays with one auth
  strategy.
- `103_target_support_explicit`: `anthropic` and raw `llama2` targets on
  `embeddings` and an `azure` target on `skills` give `BEHAVIOR_MAY_DIFFER`.
  The `skills` model is `type: api`, because a `type: model` model with
  only `skills` reverts as `type: api`.
- `104_wildcard_capabilities_multi_format`: `*` capabilities with two
  formats.
- `105_wildcard_bedrock_mantle`: a mantle `bedrock` target gets only the
  mantle routes.
- `106_wildcard_bedrock_batches`: a `bedrock` target without
  `batch_bucket_prefix` loses `batches`. That model also has an `openai`
  target, because a model with no route cannot round-trip.
- `107_realtime_log_payloads`: the `realtime` route forces
  `log_payloads: false` with a warning.
- `111_multi_format_explicit`: explicit lists with equal `realtime` paths.
- `112_wildcard_ignored_values`: values next to `*` with no issue.

Changed and unchanged cases:

- `26_multimodal_collapse_guard` gets a new `[embeddings, generate]`
  `bedrock` model: `generate` sorts first and keeps `invoke`, so
  `embeddings` gives `CONFIG_FIELD_DROPPED`.
- `108_bedrock_mantle_target_support` and `109_bedrock_mantle_files` must
  not change.

## Reverse golden cases

- `69_model_multi_format` is new: a model with routes in two sections
  reverts with both formats.
- `60_skills_api` changes because of the forced values rule.
  `10_bedrock_invoke_multitarget` does not change.
- `revert/roundtrip_test.go` runs each new forward case and must stay
  byte-identical.

## End-to-end tests

- `wildcard_model_all_endpoints_2_0` to `_2_3` are new: the data plane of
  each version must load every route of a `*` model.
  - The 2.0, 2.1, and 2.2 cases pass.
  - The 2.3 case also has a mantle `bedrock` target. It fails only on the
    Gemini Interactions routes until a data plane release ships that API.
- `gemini_interactions_api` is new: the data plane accepts a `gemini`
  target on `llm/v1/responses` in the `gemini` format. It fails until a
  data plane release ships the Interactions API.