---
description: Data plane conversion compatability
approvers:
  - team-koko
tags:
  - convert
  - compatibility
  - api
jira: https://konghq.atlassian.net/browse/KOKO-4510
---

# Motivation
[motivation]: #motivation

Today the converter makes one configuration shape for every data plane. It
assumes the newest AI Gateway release. It relies on the caller to omit fields
that older versions do not accept.

There is a new requirement to default model formats and capabilities to all
values that the data plane supports at the minimum runtime version. The existing
strategy cannot do this because the data plane rejects unknown values. The
converter must know the minimum runtime version.

The Konnect control plane (CP) already gates API features by a minimum runtime
version, as the CP/DP version compatibility RFC defines. The end user sets
`min_runtime_version` for the gateway. The API accepts only the features that
this version supports. The converter must follow the same rules. Then its
output matches what the API accepts and what the data plane can run.

# Scope
[scope]: #scope

- In scope:
  - The convert direction (`convert`).
  - Version-dependent output shapes for AI Gateway (`2.x`) versions.
- Out of scope:
  - The reverse direction (`revert`).
  - A configuration for more than one version at the same time. For a fleet
    with mixed versions, the caller passes the lowest version, as Konnect does
    with `min_runtime_version`.

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

New concepts:

- **Minimum runtime version**: the lowest AI Gateway minor release that must
  run the generated configuration, for example `2.1`. It is the same value as
  the `min_runtime_version` of the Konnect AI Gateway. It has a major and a
  minor number only. Every forward conversion needs it.
- **Feature gate**: a named converter feature with the first AI Gateway
  version that supports it.
- **Supported version range**: the oldest and the newest AI Gateway release
  that the converter knows. The oldest release, `2.0`, is the support floor.

## CLI

```sh
ai-deck-converter -min-runtime-version 2.1 input.yaml -o kong.yaml
```

A forward conversion without `-min-runtime-version` fails:

```text
error: -min-runtime-version is required when converting to decK or db-less, for example -min-runtime-version 2.1
```

## Library

```go
doc, issues, err := convert.Convert(src, convert.Options{
	MinRuntimeVersion: "2.1",
})
```

`Convert` returns an error when `MinRuntimeVersion` is empty. A Konnect component
passes the `min_runtime_version` of the gateway as `MinRuntimeVersion`.

## Example: a feature that the minimum runtime version does not have

The input has a model with an `allow_when` ACL:

```yaml
models:
  - name: gpt-5
    access:
      acls:
        allow_when:
          - 'consumer.labels.tier == "gold"'
    # ...
```

`allow_when` starts in AI Gateway `2.2`. A conversion with
`-min-runtime-version 2.1` gives this issue:

```yaml
code: UNSUPPORTED_IN_MIN_RUNTIME_VERSION
severity: ERROR
reason: 'access.acls.allow_when requires minimum runtime version "2.2", but the configured minimum is "2.1"'
source:
  kind: model
  name: gpt-5
  fields: ["access.acls.allow_when"]
```

The model is absent from the output. This follows the fail-closed rule of
RFC 00207: an access rule that the converter cannot enforce does not give an
unprotected model.

## Example: a feature that the converter can leave out

A model has the `realtime` capability and a policy whose plugin does not
accept the `ws` protocol. The converter already leaves out such a policy with
a warning. The plugin list for this check now depends on the minimum runtime
version. It is not one list for all versions.

## Example: a version-dependent shape

Assume AI Gateway `2.2` renames a field of `ai-proxy-advanced`. For the same
input, the converter writes the old field name for `2.1` and the new field
name for `2.2`. The user does not change the input.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

## New Minimum Runtime Version Option

A new `MinRuntimeVersion` option sets the lowest AI Gateway minor release
that must run the configuration. The CLI flag `-min-runtime-version` sets it.

- The option has the name, the format and the meaning of the
  `min_runtime_version` field of the Konnect AI Gateway API. A caller can pass
  that value as is.
- The converter accepts only the form `MAJOR.MINOR`, for example `2.1`. This
  is the pattern `^\d+\.\d+$` of the Konnect field.
- Versions compare by major first, then minor. `2.10` is greater than `2.9`.
- `latest` is not a valid value. The minimum runtime version is always a specific
  release.
- An empty value, a value that does not parse, or a value below the support
  floor of `2.0` fails the whole run.
- A version above the newest known release gives one `warning`. The
  converter then uses the newest known release.
- The flag is required for `to-deck` and `to-dbless`. With `auto`, it is
  required when the input is an AI Gateway document.
- With `from-deck`, the flag is not valid. The CLI returns an error.

Example:

```go
type Options struct {
	// ...
	MinRuntimeVersion string `yaml:"min_runtime_version"`
}
```

## Feature gates

`internal/aimap/versions.go` holds every version rule.
No other package compares versions.

```go
// Feature names an output of the converter that some AI Gateway releases
// do not accept.
type Feature string

const (
	FeatureACLWhen           Feature = "acl-when"
	FeatureMCPToolsetGate    Feature = "mcp-toolset-gate"
	FeaturePassthroughFormat Feature = "passthrough-format"
	// ...
)

// Version is an AI Gateway minor release.
type Version struct {
	Major int
	Minor int
}

// featureSince maps each feature to its first AI Gateway minor release.
var featureSince = map[Feature]Version{
	FeatureACLWhen:           {Major: 2, Minor: 2},
	FeaturePassthroughFormat: {Major: 2, Minor: 2},
	// ...
}

// Supports reports whether release v has feature f.
func Supports(f Feature, v Version) bool
```

See [Gates for existing outputs](#gates-for-existing-outputs) for the versions
that the spec confirms. An e2e case confirms each gate where possible.

Rules for the gates:

- A new feature that changes the output adds a `Feature` in the same change.
- A feature that every supported release has does not need a gate. When the
  support floor moves up, the gates below the floor go away.
- Data that changes per release, for example the WebSocket plugin list, is
  keyed by version. `SupportsWebSocket` gets a version argument.

### Gates for existing outputs
[gates-for-existing-outputs]: #gates-for-existing-outputs

The source of this table is the Konnect AI Gateway OpenAPI spec, release
`2.2.10`. The spec marks each gated item with one of these annotations:

- `x-min-runtime-version` on an entity or a property.
- `x-enum-min-runtime-version` on one value of an enum.

`info.x-latest-public-runtime-version` in the spec is `2.2`. The spec treats
items above it as not generally available.

Each path in "Input fields" is relative to the entity, as in RFC 00207.
"Below the gate" is the result when the minimum runtime version is lower than "Since".

| Since | Feature                             | Input fields                                                                                                                                                                                        | Below the gate                                                                                                                                                                           |
|-------|-------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `2.1` | `FeatureTargetCostList`             | `targets[n].config.input_cost_list`, `output_cost_list`, `cache_read_cost_list` on models                                                                                                           | `warning`. The field is absent. Cost tracking uses the flat cost fields.                                                                                                                 |
| `2.1` | `FeaturePolicyCondition`            | `condition` on policies                                                                                                                                                                             | `error`. The policy is absent. Without the condition, the plugin runs on every request.                                                                                                  |
| `2.1` | `FeatureMCPUpstreamProtocolVersion` | `config.server.upstream_protocol_version` on `upstream-server` MCP servers                                                                                                                          | `error`. The MCP server is absent. An upstream that answers no handshake cannot work without it.                                                                                         |
| `2.1` | `FeatureMCPAllowedVersions`         | `config.allowed_versions` on `listener` and `conversion-listener` MCP servers                                                                                                                       | `error`. The MCP server is absent. The server must not accept revisions that the user excluded.                                                                                          |
| `2.1` | `FeatureMCPCacheHints`              | `config.cache` on `listener` and `conversion-listener` MCP servers                                                                                                                                  | `warning`. The field is absent.                                                                                                                                                          |
| `2.1` | `FeatureSagemaker`                  | `targets[n].config.type: sagemaker` on models, `type: sagemaker` on model providers                                                                                                                | `error`. A `sagemaker` target on another model is absent with a `warning`. When it is the last target, the model is absent with an `error`.                                              |
| `2.2` | `FeatureACLWhen`                    | `access.acls.allow_when`, `access.acls.deny_when` on models and agents                                                                                                                              | `error`. The entity is absent. This is the fail-closed rule of RFC 00207.                                                                                                                |
| `2.2` | `FeaturePassthroughFormat`          | `formats[n].type: passthrough` on models                                                                                                                                                            | `error`. The model is absent.                                                                                                                                                            |
| `2.2` | `FeatureTypesafe`                   | `formats[n].type: typesafe` and `targets[n].config.type: typesafe` on models, `type: typesafe` on model providers                                                                                   | `error`. A model with the `typesafe` format is absent. A `typesafe` target on another model is absent with a `warning`. When it is the last target, the model is absent with an `error`. |
| `2.2` | `FeatureDecisionsCapability`        | `capabilities: [decisions]` on `type: model` models                                                                                                                                                 | `error`. The model is absent.                                                                                                                                                            |
| `2.2` | `FeatureSkillsCapability`           | `capabilities: [skills]` on `type: api` models                                                                                                                                                      | `error`. The model is absent.                                                                                                                                                            |
| `2.3` | `FeatureFailoverHTTPCodes`          | `config.balancer.failover_criteria` values `http_400`, `http_401`, `http_402`, `http_405`, `http_408`, `http_409`, `http_413`, `http_415`, `http_422`, `http_522`, `http_529`, `http_5xx` on models | `warning`. Each gated value is absent from the list. The other values stay.                                                                                                              |
| `2.3` | `FeatureBedrockVPCEndpoint`         | `targets[n].config.vpc_endpoint` on bedrock targets                                                                                                                                                 | `error`. The model is absent. Without the field, traffic goes to the public Bedrock endpoint.                                                                                            |
| `2.3` | `FeatureMCPTokenVault`              | `token_vault` on MCP servers                                                                                                                                                                        | `error`. The MCP server is absent. Without the vault, the upstream gets no credential.                                                                                                   |
| `2.3` | `FeatureMCPACLWhen`                 | `access.acls`, `access.default_tool_acls`, and `tools[n].access.acls` fields `allow_when`, `deny_when` on MCP servers                                                                               | `error` for server fields. The MCP server is absent. `warning` for tool fields. The tool is absent.                                                                                      |
| `2.3` | `FeatureGeminiAgenticCapability`    | `capabilities: [agentic]` on models with `formats: [{type: gemini}]`                                                                                                                                | `error`. The model is absent. Bedrock and OpenAI also serve `agentic`, but shipped it earlier, so it stays ungated for them.                                                             |

Note: Some gates protect outputs that are not API fields. Their versions come
from the plugin schemas of each release.

- `FeatureMCPToolsetGate`: the `pre-function` gate on conversion-only MCP
  routes.
- The WebSocket plugin list of `SupportsWebSocket`.

## Additional implementation details

- The `Converter` struct stores `minRuntimeVersion`. Each convert step uses it to
  decide whether a feature is supported.
- No endpoint has its own gate. The gated capabilities and formats, for example
  `skills` and `typesafe`, cover their endpoints. Gemini `realtime` runs on
  every supported release, so it has no gate.
- A new `UNSUPPORTED_IN_MIN_RUNTIME_VERSION` issue code reports a field that the
  minimum runtime version does not support. The rules for whether a condition is a
  `warning` or an `error` are the same as for RFC 00207.
- `ConversionMetadata` has a new `MinRuntimeVersion` field.
- `-strict` changes every warning into an error. This includes the fallback
  warning for a version above the newest known release.
- Datastores: a datastore type or a substitution path that changes per
  release uses a feature gate in `aimap.ApplyDatastore` or
  `aimap.ApplyModelDatastore`.
- `revert` takes no minimum runtime version. It calls the shared `aimap` lookups with
  the newest known release, so it recognizes every output shape.

# Drawbacks
[drawbacks]: #drawbacks

- Each new feature needs a version.
- Each entity conversion has additional version rules, which adds complexity.
- The required `-min-runtime-version` is a breaking change.
- Gates assume that the runtime follows semver. A feature in one release stays
  in every later release of the same major version. A breaking change at a
  major version needs new gates.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

One converter serves all releases. All version rules live in one file, and
issues use the RFC 00207 model. The option is required because a default
hides a version mismatch until sync time. The value is an explicit user
choice, as in the CP/DP RFC. The option name, the `MAJOR.MINOR` format and the
"lowest version" meaning match the Konnect `min_runtime_version` field. Patch
releases add no plugin fields, so they need no gates.

Alternatives:

- **Optional version with a default.** The default needs an update for every
  release.
- **One converter release per data plane release.** The Konnect control plane
  must run several converter versions.
- **Always emit the newest shape.** Data planes reject unknown config at sync
  time.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

# Testing
[testing]: #testing

- Golden cases accept `min_runtime_version` in `options.yaml`.
- Each gate has two golden cases: one at the first supported version, one
  below it. The case below records the expected `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`
  issue.
- The golden test harness uses the newest known release when `options.yaml`
  does not set `min_runtime_version`. The output of the existing cases does not
  change. This default exists only in tests.
- `internal/aimap/versions_test.go` checks the version parser, the fallback
  rules, and that every `Feature` has an entry in the table. Parser cases
  include `2.1` (valid), and `2.1.0`, `2.1.0-rc.4`, `02.1`, `2`, `latest`,
  and the empty value (not valid). Comparison cases include `2.10` greater
  than `2.9`.
- Unit tests check that `Convert` and the CLI fail without a minimum runtime version,
  and that the CLI rejects `-min-runtime-version` with `from-deck`.
- Each e2e case passes the minor release of its default image as the minimum
  runtime version. For example, `kong/kong-ai-gateway:2.0.2` gives `2.0`.
- An e2e case for each gate loads the output for a minimum runtime version below the gate into
  an image of that version. It checks that the data plane accepts it.
