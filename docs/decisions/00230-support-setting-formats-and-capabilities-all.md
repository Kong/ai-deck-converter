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
selects all possible endpoints.

# Scope
[scope]: #scope

- In scope: `*` in `capabilities` and `formats`, and models with more than
  one format.
- Out of scope: the `passthrough` format. `*` never selects it.

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

### Case 1: all possible endpoints

```yaml
models:
  - name: a              # every capability except batches and files
    capabilities: ["*"]
    formats: [{type: "*"}]
  - name: b
    type: api            # only batches and files
    capabilities: ["*"]
    formats: [{type: "*"}]
```

### Case 2: all possible capabilities for the given formats

```yaml
models:
  - name: a
    formats: [{type: cohere}, {type: openai}]
    capabilities: ["*"]
```

The model serves all possible capabilities of each format.

### Case 3: targets with different providers

The provider of a target changes the capabilities that `*` selects for it.
The model serves each capability that at least one target serves. A route
balances only over the targets that serve its capability.

```yaml
models:
  - name: a
    formats: [{type: openai}]
    capabilities: ["*"]
    targets:
      - name: primary
        provider: openai-prod   # serves skills
      - name: fallback
        provider: azure-prod    # cannot serve skills
```

The `skills` route has only the `primary` target. A failure on this route
cannot fail over to `fallback`. The other routes have both targets. The
converter gives no issue. An explicit `skills` capability gives a warning
on `fallback`. The other sections of this RFC use this example.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

## Model fields

`*` is a new value of `capabilities` and of `Format.Type`.

When a list holds `*`, the converter ignores every other value of that list.
`["*", generate]` is equal to `["*"]`. All other checks use this effective
list. `[{type: "*"}, {type: passthrough}]` is therefore not a passthrough
model.

Each format that the converter uses must be `*` or a value of
`aimap.Formats()`. Any other value fails the model. Today an unknown format
fails only when no endpoint serves its capabilities.

| Field          | Absent or `[]`     | `*` value             |
|----------------|--------------------|-----------------------|
| `formats`      | `[{type: openai}]` | every wildcard format |
| `capabilities` | no capability      | every capability      |

## Expansion of `*`

`internal/aimap` owns the expansion of `*` to formats and capabilities:

```go
// Wildcard is the formats and capabilities value that selects every value.
const Wildcard = "*"

// WildcardFormats returns the formats that "*" selects for the minimum
// runtime version. It also returns the formats that the version gates
// leave out.
func WildcardFormats(minRuntimeVersion Version) (selected, unsupported []string)

// WildcardCapabilities returns the capabilities that "*" selects for one
// format, provider type, model type, and minimum runtime version.
func WildcardCapabilities(
	format, providerType, modelType string, minRuntimeVersion Version,
) []string
```

The provider type changes the result. The converter therefore expands the
lists for each target.

`WildcardFormats` returns `aimap.Formats()` with `openai` first. It leaves
out these formats:

- `passthrough`.
- A format with a `FormatFeature` gate that `minRuntimeVersion` does not
  support. The function returns it in `unsupported`.

`WildcardCapabilities` starts from `aimap.CapabilitiesFor(format,
providerType)`. That function already leaves out a passthrough-only
capability, for example `skills`, when the provider does not speak the
format. `WildcardCapabilities` then leaves out these capabilities:

- For a `type: model` model, `batches` and `files`.
- For a `type: api` model, every other capability.
- A capability with a `CapabilityFeature` gate that `minRuntimeVersion`
  does not support.
- A capability whose endpoint shares a route with an earlier capability of
  another genai category. One `ai-proxy-advanced` plugin serves one genai
  category. For example, `generate` comes first on the `bedrock` `invoke`
  route. `embeddings`, `image`, and `video` therefore do not get this
  route.

The converter also normalizes explicit formats and removes duplicates.

Today a capability that the format does not serve fails the model. With this
RFC, the converter leaves out the pairs that it cannot serve. See
[Issues](#issues).

## More than one format

Today the converter reads only `formats[0]`. With this RFC, it converts every
format. Route groups already use the section in their key. Routes of different
formats therefore never merge. Each `ai-proxy-advanced` plugin carries the
`llm_format` of its route. `ai_models` entries do not change. The video
lifecycle route checks for an `openai` `video` pair, not for `formats[0]`.

### Pair order

The output lists routes in pair order. This order must not depend on the
order of the lists, otherwise the round trip fails. When a model has more
than one format or uses `*`, the converter uses a fixed pair order:

- Formats in `WildcardFormats` order. `openai` is first. The other formats
  follow in sorted order.
- For each format, capabilities in `CapabilitiesFor` order. `generate` is
  first. The other capabilities follow in sorted order.

A model with one explicit format and explicit capabilities keeps the list
order, as today.

### Equal paths in two formats

`openai` and `gemini` both serve `realtime` on `/realtime`. Two formats of
one model can give equal paths and protocols. The model keeps the endpoint
of the earlier format in pair order. This format is always `openai`.

## `realtime` under `*`

Two conditions prevent the protection of a WebSocket route:

- A global authentication policy. A global plugin keeps the `http` and
  `https` protocols. It cannot protect `ws` or `wss`.
- More than one auth strategy on the model. A WebSocket route has no
  `anonymous` fallback. Each strategy rejects the clients of the others.

One auth strategy of any type protects the route, for example `jwt`. Under
`*`, the converter leaves out only the `realtime` endpoint.

## Version gates

`*` is a converter feature. The output holds only routes and plugins that
exist today. `*` therefore gets no `Feature` in
`internal/aimap/versions.go`.

## Video lifecycle routes

Today `videoLifecycleCandidates` gives two warnings for a video model:

- A model with more than one target gets a warning. A request without a
  model alias can reach a target other than the target that made the job.
- Two video models on one route each get a warning.

When `video` comes from `*`, the model gets neither warning. The output does
not change. A `*` model still counts toward a shared route. A model with an
explicit `video` capability on that route therefore still gets its warning.
The warning names every video model on the route.

## Issues

Two rules decide if `*` gives an issue:

1. **No issue when `*` causes the result.** The model does not ask for the
   value.
2. **An issue when the model or a target cannot serve an explicit value as
   asked.** The user asked for the value.

Note: an unknown format gives an issue outside these rules.

| Case                                                                                                                                 | Under `*`                  | Explicit list                                                                       | Code and field                                                                              | Rule |
|--------------------------------------------------------------------------------------------------------------------------------------|----------------------------|-------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------|------|
| `type` leaves out a capability                                                                                                       | left out, no issue         | not changed                                                                         |                                                                                             | 1    |
| Passthrough-only pair, for example `(openai, skills)` on `anthropic`. The target serves the capability through another format        | pair left out, no issue    | pair left out, no issue                                                             |                                                                                             | 1    |
| Pair that no endpoint serves, when another pair serves the capability, for example `(cohere, generate)` next to `(openai, generate)` | left out, no issue         | left out, no issue                                                                  |                                                                                             | 1    |
| Capability that shares a route with another genai category, for example `(bedrock, embeddings)` on the `invoke` route                | left out, no issue         | left out, no issue when another endpoint serves the capability. Otherwise `WARNING` | `CONFIG_FIELD_DROPPED` on the capability                                                    | 1, 2 |
| Gated format or capability                                                                                                           | left out, no issue         | model fails                                                                         | `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`                                                        | 1, 2 |
| Equal paths in two formats                                                                                                           | `openai` kept, no issue    | `openai` kept, no issue                                                             |                                                                                             | 1    |
| Target cannot serve a capability that another target serves. See Case 3                                                              | target left out, no issue  | target left out, `WARNING`                                                          | `BEHAVIOR_MAY_DIFFER` on `capabilities[i]` and `targets[j]`                                 | 1, 2 |
| Video lifecycle route with 2+ targets                                                                                                | no issue                   | `WARNING`                                                                           | `BEHAVIOR_MAY_DIFFER` on `targets`                                                          | 1, 2 |
| Two video models on one route                                                                                                        | no issue for the `*` model | `WARNING`                                                                           | `ROUTING_CONFLICT` on `config.route`                                                        | 1, 2 |
| `realtime` that the model cannot protect                                                                                             | left out, no issue         | model fails                                                                         | `CONFIG_FIELD_INVALID`                                                                      | 1, 2 |
| No target serves a capability                                                                                                        | N/A                        | model fails                                                                         | `CONFIG_FIELD_INVALID` on the capability                                                    | 2    |
| Only gated formats serve a capability. For future formats                                                                            | N/A                        | model fails                                                                         | `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`                                                        | 2    |
| A value next to `*`                                                                                                                  | value ignored, no issue    | N/A                                                                                 |                                                                                             | 1    |
| Unknown format                                                                                                                       | model fails                | model fails                                                                         | `CONFIG_FIELD_INVALID` on `formats[i].type`                                                 |      |

The expansion runs for each target. The converter reports each issue one
time for each model. The exception is the "target cannot serve" warning. The
converter reports it one time for each capability and target pair.

## Reverse conversion

The reverter never writes `*`. It writes the explicit lists that the
converter expanded. It collects formats across the routes of a model group,
in route order, as it collects capabilities today.

### Alias-less groups

A `type: api` model with `*` makes `batches` and `files` routes in several
formats. Today the reverter keys an alias-less group by the route name. It
merges only routes with the same capability. The routes of one model
therefore give two groups. A `type: api` model with `[batches, files]` has
this problem today. This fix can ship separately.

The reverter keys an alias-less group by the target set and the base paths
of the route. The target set is the sorted list of target fingerprints. It
uses this key only when the groups and the free `ai_models` entries then
have equal counts, otherwise it keys by the route name, as today. This
keeps apart two models with the same target and different capabilities.

### Round trip

The collected formats and capabilities give the same pairs again. The
[pair order](#pair-order) makes the order of the lists irrelevant.
The round trip case that `revert/roundtrip_test.go` asserts stays 
byte-identical.

The round trip can add warnings. Under `*`, rule 1 gives no issue. The
reverter writes explicit lists. The second conversion then applies rule 2.
`unexpectedIssues` accepts these `WARNING` issues, only for a model with
`capabilities: ["*"]` in the forward input:

- `BEHAVIOR_MAY_DIFFER` on one `capabilities[i]` and one `targets[j]`.
- `BEHAVIOR_MAY_DIFFER` on `targets`.
- `ROUTING_CONFLICT` on `config.route`.

Only `videoLifecycleCandidates` gives the last two.

# Drawbacks
[drawbacks]: #drawbacks

- **Existing configurations can convert differently.**
  - An unknown format now fails the model. Today a model with an unknown
    format converts with no issue in two cases: with `capabilities: []`,
    and after `formats[0]`.
  - A model with more than one format now gets the routes of each format.
- **Output changes with the converter release.** When a release adds an
  endpoint, each model with `*` gets it.
- **Many generated routes.** On the latest runtime version, one
  `type: model` model on an `openai` provider makes 11 routes with
  `capabilities: ["*"]` and 29 routes with `*` for both lists. Both counts
  include the video lifecycle route.
- **More routing conflicts.** A `*` model uses every path under its base
  path. An MCP server or an agent on such a path conflicts with the model.
- **Results under `*` that give no issue.** See rule 1 of
  [Issues](#issues). The user gets no signal for these results:
  - Failover differs for each endpoint. See Case 3.
  - A video lifecycle request can reach a wrong target.
  - Auth settings remove the `realtime` endpoint.
  - A later runtime version adds endpoints.
  - A value next to `*` is ignored. For example, the user can expect
    `[{type: "*"}, {type: passthrough}]` to be a passthrough model.
  - A `bedrock` model serves `generate` on `invoke`, never `embeddings`,
    `image`, or `video`. A Bedrock embedding or image model needs an
    explicit list.
- **Untested format and provider pairs.** `formats: [{type: "*"}]` asks one
  provider to serve every format.
- **`type: api` models on one route stay merged after a revert.** These
  models share one plugin. A target cannot carry a tag. Therefore, the
  reverter cannot split two such models with equal base paths and shared
  capabilities. This occurs today. `*` makes it more frequent.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

- **Default absent or empty lists to every value.** An empty `capabilities`
  list can show that the user wants no value. An absent `formats` list
  means `openai` today. A change adds routes to every existing model.
- **A boolean field, for example `all_capabilities: true`.** The
  configuration then uses two fields for one list.
- **Select only the capabilities that all targets serve.** Each route then
  balances over all targets. A target with fewer capabilities then removes
  endpoints from the whole model. This RFC uses the union.
- **Warn for each value that `*` leaves out or changes.** This covers gated
  values, a target that cannot serve a capability, the video lifecycle
  warnings, `realtime` that the model cannot protect, and a value next to
  `*`. The user cannot remove these warnings without removing `*`.
  `-strict` callers then cannot use `*`. One warning for each model has the
  same problem. This RFC follows rule 1 instead.
- **Never warn for a target that cannot serve a capability.** The round
  trip then needs no exception. The user gets no signal that failover
  differs. This RFC warns only for explicit lists. See rule 1 of
  [Issues](#issues).

# Unresolved questions
[unresolved-questions]: #unresolved-questions

# Implementation notes
[implementation-notes]: #implementation-notes

- `expandCapabilities` becomes `modelEndpoints(m, providerType)`. It returns
  the ordered pairs of one target. `convertModel` and
  `videoLifecycleCandidates` both call it.
- Each `(format, capability)` pair gets its endpoints from
  `aimap.EndpointsFor`. The section comes from the format of the pair.
- `checkModelCapabilities` gets only the explicit capabilities.
- `isPassthrough`, the passthrough format check, and `llmFormat` read the
  effective format list.
- The check for more than one auth strategy on a WebSocket route moves next
  to the global policy check. The converter then never makes a `realtime`
  route that it must remove.
- The comment of `sharedLifecycleRoutes` in `convert/lint.go` says that
  `videoLifecycleCandidates` reports equal lifecycle routes. Under `*`, no
  function reports them. The comment must tell this.
- The reverter makes the target set of a route before it reads the targets
  of the route.
- The genai category check in `convertModel` gives `CONFIG_FIELD_DROPPED`
  only when no other endpoint of the model serves the capability. The
  other endpoint can be in another format or in the same pair. For
  example, `converse` still serves `generate` when `invoke` is lost.

# Testing
[testing]: #testing

Forward golden cases in `convert/testdata`:

- `87_min_runtime_wildcard_below_gate`: a `*` model at 2.1. No gated value,
  no issue.
- `91_wildcard_all`: `*` for both lists on one `openai` target. Every
  expanded route. `gemini` `realtime` left out with no issue. `bedrock`
  `embeddings`, `image`, and `video` left out with no issue. The round trip
  gives no issue for them.
- `95_wildcard_capabilities_multi_format`: `capabilities: ["*"]` with two
  formats. Two `type: api` models on different base paths, one with `*`
  and one with `[batches, files]`. Each gives one model after the round
  trip.
- `96_multi_format_explicit`: explicit formats and capabilities. A pair
  that no endpoint serves. Equal `realtime` paths.
- `97_wildcard_ignored_values`: `["*", generate]` and
  `[{type: "*"}, {type: passthrough}]`. No issue.
- `98_wildcard_issues`: an unknown format and a misspelled capability with
  `*` formats.
- `99_multi_format_order_and_normalization`: the fixed pair order for
  `[gemini, openai]` and `(openai, skills)` on an `anthropic` provider.
- `100_wildcard_realtime_global_auth`: a `*` model and a global
  authentication policy. The model loses `realtime` with no issue. Separate
  because a global policy applies to every model.
- `102_wildcard_realtime_auth_strategies`: a `*` model with two auth
  strategies loses `realtime` with no issue. A `*` model with one `jwt`
  strategy keeps `realtime`.
- `101_wildcard_targets_and_video`: the Case 3 model with explicit lists
  and with `*`. Two `*` models and one explicit `video` model on their own
  base path. The round-trip tests the accepted warnings.

The existing model of `26_multimodal_collapse_guard` keeps its warnings. It
has only the `bedrock` format. No other endpoint serves its dropped
capabilities. A new model in this case has `formats: [{type: bedrock}]` and
`[embeddings, generate]`. `generate` loses `invoke` with no issue.
`converse` still serves it.

Reverse golden case `revert/testdata/69_model_multi_format`: one model with
routes in two sections. The expected model has both formats in route order.

End-to-end tests in `e2e/`: one case for each data plane version 2.0, 2.1,
2.2, and 2.3. The data plane accepts the routes that `*` expands to.
