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

A model gets its endpoints from its capabilities and formats. Today the user
must write each capability and each format. The user cannot select all the
endpoints that a model can serve. When a release adds a capability or a
format, the user must update these lists.

Users ask for a `*` value in the capabilities and formats lists. With `*`, the
model serves all possible endpoints.

# Scope
[scope]: #scope

- In scope:
  - Set the capabilities of a model to `*`.
  - Set the formats of a model to `*`.
  - Convert a model that has more than one format.
- Out of scope:
  - The `passthrough` format. It ignores capabilities. It cannot be
    combined with other formats.

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

New concepts:
- `*` capability: The model serves every capability of its formats.
- `*` format: The model serves all possible formats.

### Case 1: all possible endpoints

Set capabilities to `["*"]` and formats to `[{type: "*"}]`. The model then
serves all possible endpoints.

```yaml
models:
  - name: a
    capabilities: ["*"]  # all possible capabilities
    formats: [{type: "*"}]  # all possible formats
```

The type of the model changes the result. A `type: model` model gets every
capability except `batches` and `files`. A `type: api` model gets only
`batches` and `files`.

```yaml
models:
  - name: b
    type: api
    capabilities: ["*"]  # batches and files only
    formats: [{type: "*"}]
```

### Case 2: all possible capabilities for the given formats

Set the formats and set capabilities to `["*"]`. The model then serves all
possible capabilities of the given format.

```yaml
models:
  - name: a
    formats: [{type: cohere}]
    capabilities: ["*"]  # all possible capabilities
```

With more than one format, the model serves all possible capabilities of
each format.

```yaml
models:
  - name: a
    formats: [{type: cohere}, {type: openai}]
    capabilities: ["*"]  # all possible capabilities
```

An absent or empty formats list gives the `openai` format, as today. The
model below serves all possible capabilities of the `openai` format.

```yaml
models:
  - name: a
    formats: []
    capabilities: ["*"]
```

### Case 3: nothing to serve

An empty capabilities list gives no capability. The model then serves
nothing.

```yaml
models:
  - name: a
    formats: [{type: cohere}]
    capabilities: []
```

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

## Model fields

`*` is a new value for two fields of a model:

- `capabilities: ["*"]`
- `formats: [{type: "*"}]`

`formats` stays a list of `Format` structs. `*` is a new value of
`Format.Type`.

When a list holds `*`, the converter ignores every other value of that list.
`["*", generate]` is equal to `["*"]`. `[{type: "*"}, {type: openai}]` is
equal to `[{type: "*"}]`. The converter reports a `CONFIG_FIELD_IGNORED`
`WARNING` on each ignored value, for example on `capabilities[1]`.

The converter replaces each list with this effective list before all other
checks. These checks read the effective list, not the raw `m.Formats`:

- `isPassthrough`
- the check that a passthrough model has one format
- `llmFormat`

Without this step, `[{type: "*"}, {type: passthrough}]` fails as a
passthrough model with two formats.

Each format that the converter uses must be one of these values:

- `*`
- a value of `aimap.Formats()`
- a format alias, for example `vertex`

Any other value fails the model with `CONFIG_FIELD_INVALID` on
`formats[i].type`. Today an unknown format fails only when no endpoint
serves its capabilities. With `capabilities: ["*"]`, an unknown format
expands to no pair. Without this check, a typo gives no issue.

An absent list and an empty list have the same result, as today:

| Field          | Absent or `[]`     | `*` value             |
|----------------|--------------------|-----------------------|
| `formats`      | `[{type: openai}]` | every wildcard format |
| `capabilities` | no capability      | every capability      |

## Expansion

`internal/aimap` owns the expansion:

```go
// Wildcard is the formats and capabilities value that selects every value.
const Wildcard = "*"

// WildcardFormats returns the formats that "*" selects.
func WildcardFormats() []string

// WildcardCapabilities returns the capabilities that "*" selects for one
// format, provider type, and model type.
func WildcardCapabilities(format, providerType, modelType string) []string
```

The provider type of a target changes the result. The converter therefore
expands the lists for each target. `expandCapabilities` becomes
`modelEndpoints(m, providerType)`. It returns the ordered
`(format, capability)` pairs of one target. `convertModel` and
`videoLifecycleCandidates` both call it. The two paths therefore always get
the same pairs.

`checkModelCapabilities` fails the model for a gated capability. Today it
gets the capabilities of the whole model. With this RFC, it gets only the
explicit capabilities. The `*` expansion applies the gates itself and warns.
See [Version gates](#version-gates).

### Formats

`WildcardFormats` returns `aimap.Formats()` without these formats:
- `passthrough`. It cannot be combined with other formats.
- Format aliases, for example `vertex`.

`aimap.DefaultLLMFormat`, `openai`, is first. The other formats follow in
sorted order. The minimum runtime version can lack the `FormatFeature` gate
of a format. The converter then leaves out the format with a warning. See
[Version gates](#version-gates). For example, `*` selects `typesafe` only
from AI Gateway 2.2.

### Capabilities

`WildcardCapabilities` first changes the format with `aimap.NormalizeFormat`.
For example, `vertex` becomes `gemini`. `CapabilitiesFor` returns nil for an
alias. Without this step, `formats: [{type: vertex}]` with
`capabilities: ["*"]` serves nothing. It also gives no issue.

The converter also normalizes explicit formats before it pairs them. It
removes a format that occurs two times after normalization.

`WildcardCapabilities` then starts from
`aimap.CapabilitiesFor(format, providerType)`. That function already leaves
out a passthrough-only capability, for example `skills`. It does this when
the provider does not speak the format. `WildcardCapabilities` then leaves
out these capabilities:

- For a `type: model` model, the `type: api` capabilities `batches` and
  `files`.
- For a `type: api` model, every other capability.
- A capability with a `CapabilityFeature` gate that the minimum runtime
  version does not support. The converter reports a warning. See
  [Version gates](#version-gates).

The first two rules give no issue. `CapabilitiesFor` also gives no issue.
The model can never serve these capabilities. It therefore loses no
endpoint.

The order stays the order of `CapabilitiesFor`. `generate` is first. The
other capabilities follow in sorted order. See [Pair order](#pair-order) for
explicit lists.

### Pairs that no endpoint serves

Today a capability that the format does not serve fails the model. With more
than one format, these rules apply:

- The converter leaves out a pair that no endpoint serves. It gives no
  issue. The model loses nothing.
- The converter also leaves out a pair with a passthrough-only capability
  when the provider of the target does not speak the format of the pair.
  For example, an `anthropic` provider leaves out `(openai, skills)`. It
  keeps `(anthropic, skills)`. Today `convertModel` fails the model for this
  pair. With this RFC, the model fails only when no declared format of the
  target can serve the capability.
- An explicit capability must have at least one pair that an endpoint
  serves. If no pair is served, the converter fails the model with
  `CONFIG_FIELD_INVALID` on the capability. This rule applies to explicit
  formats and to `formats: [{type: "*"}]`.
- When only gated formats serve the capability, the converter fails the
  model with `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`.

A model with one format therefore fails as today. A misspelled capability
also fails when formats is `*`.

## More than one format

Today the converter reads only `formats[0]`. With this RFC, it converts every
format. Each `(format, capability)` pair gets its endpoints from
`aimap.EndpointsFor`. The section comes from the format of the pair.

Route groups already use the section in their key. Routes of different
formats therefore never merge. Each `ai-proxy-advanced` plugin carries the
`llm_format` of its route. `ai_models` entries do not depend on the format.
They do not change. The video lifecycle route checks for an `openai` `video`
pair. It does not check `formats[0]`.

### Pair order

The converter makes routes in pair order. The output lists the routes in
that order. This order must not depend on the order of the lists. If it
does, the round trip fails.

For example, a `*` model makes the `gemini` `rerank` route before the
`gemini` `video` route. The reverter collects capabilities in route order.
The `openai` routes come first. The reverter therefore writes `video` before
`rerank`. A conversion in list order then swaps the two `gemini` routes.

When a model has more than one format or uses `*`, the converter uses a
fixed pair order:

- Formats in `WildcardFormats` order. `openai` is first. The other formats
  follow in sorted order.
- For each format, capabilities in `CapabilitiesFor` order. `generate` is
  first. The other capabilities follow in sorted order.

A model with one explicit format and explicit capabilities keeps the list
order, as today. Its output does not change.

### Equal paths in two formats

`openai` and `gemini` both serve `realtime` on `/realtime`. Two formats of
one model can give equal paths and protocols. The model then keeps the
endpoint of the earlier format in [pair order](#pair-order). This format is
always `openai`.

The converter gives a `ROUTING_CONFLICT` `WARNING` on the left-out format.
It does this only when the formats and the capability are explicit. RFC
00229 uses this code for two entities. Here, the two endpoints belong to one
model.

When `formats` or `capabilities` is `*`, the converter gives no issue. The
model still serves the endpoint. The user cannot remove the warning without
`*`. The warning therefore occurs on every conversion.

## Omitted values

- **`passthrough`.** `formats: [{type: "*"}]` never selects it.
  `[{type: "*"}, {type: passthrough}]` is not a passthrough model. The
  converter reports the ignored `passthrough` value. See
  [Model fields](#model-fields). A `passthrough` model already ignores its
  capabilities. `capabilities: ["*"]` therefore changes nothing on it.
- **`realtime` when the model cannot protect it.** A global authentication
  policy or an auth strategy can fail to protect a WebSocket route. An
  explicit `realtime` capability then fails the model. With
  `capabilities: ["*"]`, the converter leaves out only the `realtime`
  endpoint. It reports a `BEHAVIOR_MAY_DIFFER` warning.

## Version gates

`*` is a converter feature. The output holds only routes and plugins that
exist today. `*` therefore gets no `Feature` in
`internal/aimap/versions.go`.

The expansion leaves out gated formats and capabilities. The converter
reports a `BEHAVIOR_MAY_DIFFER` `WARNING` on `formats` or `capabilities` for
each value that it leaves out. The model therefore serves every endpoint
that the minimum runtime version supports. An explicit gated value still
fails the model with `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`.

## Issues

| Condition                                        | Code                                 | Severity  | Result             |
|--------------------------------------------------|--------------------------------------|-----------|--------------------|
| An unknown format                                | `CONFIG_FIELD_INVALID`               | `ERROR`   | model left out     |
| No format serves an explicit capability          | `CONFIG_FIELD_INVALID`               | `ERROR`   | model left out     |
| Only gated formats serve an explicit capability  | `UNSUPPORTED_IN_MIN_RUNTIME_VERSION` | `ERROR`   | model left out     |
| Explicit formats and capability give equal paths | `ROUTING_CONFLICT`                   | `WARNING` | later endpoint out |
| `*` capabilities cannot protect `realtime`       | `BEHAVIOR_MAY_DIFFER`                | `WARNING` | endpoint left out  |
| `*` selects a gated format or capability         | `BEHAVIOR_MAY_DIFFER`                | `WARNING` | value left out     |
| A value next to `*`                              | `CONFIG_FIELD_IGNORED`               | `WARNING` | value ignored      |

The expansion runs one time for each target. The converter reports each
issue one time for each model. For example, a model with three targets can
leave out the gated `skills` capability. This model gets one warning, not
three. The converter first collects the left-out values of all targets. It
then reports each value one time.

## Reverse conversion

The output does not show if the user wrote `*`. The reverter therefore never
writes `*`. It writes the explicit lists that the converter expanded.

The reverter needs one change. Today `revert/model.go` sets one format when
it makes a model group. A model with more than one format has routes in more
than one section. The reverter must therefore collect formats across the
routes of the group, in route order. It already collects capabilities in
this way.

Forward conversion makes every served pair of the two lists. The collected
formats and capabilities therefore give the same pairs again. A model with
more than one format uses the fixed [pair order](#pair-order). The order of
the collected lists therefore has no effect. A model with one format keeps
its list order. The reverter collects capabilities in that order. In both
cases, `revert/roundtrip_test.go` stays byte-identical.

# Drawbacks
[drawbacks]: #drawbacks

- **Output changes with the converter release.**
  - When a release adds an endpoint, each model with `*` gets the new
    endpoint.
- **Many generated routes.**
  - One `*` model on an `openai` provider makes approximately 25 routes.
- **More routing conflicts.**
  - A `*` model uses every path under its base path, for example
    `/v1/messages`. An MCP server or an agent on such a path conflicts with
    the model.
- **More warnings.**
  - A `*` model below the latest runtime version gets a warning for each
    gated value. This occurs on every conversion.
  - `-strict` exits with code 1 on any issue. `-strict` callers therefore
    cannot use these `*` models.
- **Untested format and provider pairs.**
  - `formats: [{type: "*"}]` asks one provider to serve every format. The
    provider can lack support for some formats.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

With this design, a user can select all formats and capabilities in one
model. The user can also write explicit lists of formats and capabilities.

Alternatives:
- **Default absent/empty lists to every value**
  - For `capabilities`, an absent or empty list means no capability today.
    It can show that the user wants no value. `*` is therefore an explicit
    opt-in.
  - For `formats`, an absent or empty list means `openai` today. A change
    adds routes to every existing model without `formats`.
- **A boolean field, for example `all_capabilities: true`**
  - Callers must set an additional field. The configuration of formats and
    capabilities then uses two fields. This RFC does not use a boolean
    field for these reasons.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

# Testing
[testing]: #testing

Forward golden cases in `convert/testdata`:

- `93_wildcard_all`: `formats: [{type: "*"}]` and `capabilities: ["*"]` on
  one `openai` target. The case checks every expanded route. It also checks
  that the converter leaves out the `gemini` `realtime` endpoint with no
  issue.
- `94_wildcard_capabilities_multi_format`: `capabilities: ["*"]` with
  `formats: [{type: cohere}, {type: openai}]`. It also covers a `type: api`
  model. This model gets only `batches` and `files`.
- `95_multi_format_explicit`: two explicit formats and explicit
  capabilities. It covers a pair that no endpoint serves. It also covers the
  `ROUTING_CONFLICT` warning for `openai` and `gemini` `realtime`.
- `96_wildcard_ignored_values`: `["*", generate]` and
  `[{type: "*"}, {type: passthrough}]`. Each ignored value gets one
  `CONFIG_FIELD_IGNORED` warning.
- `97_wildcard_issues`: models that fail or warn. It covers these inputs:
  - an unknown format
  - a misspelled capability with `*` formats
  - a `realtime` endpoint that a `jwt` auth strategy cannot protect
- `98_min_runtime_wildcard_below_gate`: a `*` model with
  `min_runtime_version: "2.1"` in `options.yaml`. It covers these results:
  - the gated-value warnings, one for each model with more than one target
  - an explicit capability that only a gated format serves
- `99_multi_format_order_and_normalization`: three rules that no other case
  covers.
  - Explicit `[{type: gemini}, {type: openai}]` gives the `openai` routes
    first, in the fixed pair order.
  - `formats: [{type: vertex}]` with `capabilities: ["*"]` serves every
    `gemini` endpoint.
  - `[{type: openai}, {type: anthropic}]` with `[generate, skills]` on an
    `anthropic` provider leaves out `(openai, skills)`. It keeps
    `(anthropic, skills)`.

Reverse golden case in `revert/testdata`:

- A decK config with one model. The routes of the model are in two
  sections. The expected AI Gateway model has both formats in route order.

`revert/roundtrip_test.go` runs on every forward case. The `93_` to `95_`
cases therefore also test that the reverter collects formats across routes.

End-to-end tests in `e2e/`:

- Add an e2e test case for each data plane version: 2.0, 2.1, 2.2, and 2.3.
  The case tests that the data plane accepts the configuration when formats
  and capabilities are `*`.
