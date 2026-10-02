---
description: Structured conversion issue reporting
approvers:
  - #add at least one team to review
tags:
  - convert
  - error-handling
  - api
jira: https://konghq.atlassian.net/browse/KOKO-3890
---

# Motivation
[motivation]: #motivation

Many components use ai-deck-converter to generate Kong Gateway configuration.
Each component has its own rules for issues. Some issues must stop the
conversion. Other issues only notify the user.

Today the converter reports issues in two ways:

- `warn` records a warning as a string, and the conversion continues.
- `failAt` returns an error, and the conversion stops.

One global setting, `Options.Strict`, changes every warning into an error. The
`-strict` CLI flag sets it. A caller cannot escalate only some warnings. A
caller also cannot see the class of a warning without parsing its message text.

# Scope
[scope]: #scope

- What is considered in scope for this RFC?
  - issues that occur during convert
- What is considered out of scope for this RFC?
  - issues that occur during revert

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

The conversion functionality returns the generated Kong Gateway configuration
together with a list of the issues that occurred during conversion.
The caller decides how to handle each issue.

The expected conversion behavior is:

1. A caller calls the conversion functionality with an input YAML document.
2. The converter converts each resource.
   1. If an issue occurs, the converter adds it to the conversion result.
3. After the last resource, the converter returns the configuration and the
   issues.

New concept: issues

- **Issue**: a condition that the converter reports during conversion.
- **Issue severity**: the level of an issue.
  - `warning`: the converter worked around the issue. The entity is in the
    generated configuration. A nested item of the entity can be absent.
  - `error`: the converter could not work around the issue. The affected
    entity is absent from the generated configuration.

Severity belongs to each issue, not to its code. One code can occur with
either severity. For example, a missing required field on a nested item is a
`warning`. The same issue on the entity is an `error`.

### Example issues

- Issues that are usually errors:
  - Missing Required Field
  - Internal Error
    - Covers converter bugs that affect one entity. A panic in one entity gives
      this issue.
  - Undefined Reference
  - Value Invalid
    - If the converter can still make the entity, use Behavior May Differ.
    - Examples:
      - entity conflict: two entities need one generated resource. No valid
        configuration exists for both.
      - incompatible fields
- Issues that are usually warnings:
  - Value Dropped
    - Covers values or entities that the converter removes, for example
      duplicate aliases or an unused conversion-only MCP server.
  - Value Ignored
    - Covers values that the converter does not support.
    - If the converter cannot make the entity, use Value Invalid.
  - Value Overridden
    - Covers a user value that the converter replaces with a forced value or a
      fallback.
    - A default that the converter applies to an unset field is not an issue.
    - If the converter cannot make the entity, use Value Invalid.
  - Behavior May Differ
    - The configuration loads. It may not do what the user expects.
    - Examples:
      - usage or cost is not extracted
      - a policy may not see the prompt
      - a request may reach the wrong target
      - routes overlap: the configuration is valid. A request can match more
        than one route.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

To implement this RFC, the converter needs:

- Per-conversion state that stores every issue found during one conversion.
  The state lives on the `Converter`, not at package level. Concurrent
  conversions stay independent.
- A function that conversion steps call to add an issue to that state.
  - After an issue, each conversion step continues.
  - Rules for partial output:
    - The converter leaves out a nested list item that it cannot convert. It
      reports a `warning`. The rest of the entity still converts.
    - For example, a tool without a description is absent. The other tools
      of the MCP server still convert.
    - Auth strategy references are an exception to the nested item rule. The
      auth strategy rule below applies to them.
    - The `warning` uses the code of the cause, not `VALUE_DROPPED`. Its
      `Fields` path points at the item. For example, a tool without a
      description gives `MISSING_REQUIRED_FIELD` with the path
      `tools[2].description`.
    - Sometimes the entity is not valid without the left-out items. Then the
      converter leaves out the entity and reports an `error`. For example, a
      model whose targets are all left out is absent.
    - When an entity references an auth strategy that does not exist or
      cannot be converted, the entity fails closed. The converter leaves out
      the entity and reports an `error`. For example, a model whose auth
      strategy is unresolved is absent, not unprotected.
    - When a global or referenced policy cannot be converted, the converter
      still generates the configuration and reports an `error`.
- A function that returns the collected issues to the caller.

Issues use this structure:

```go
type ConversionIssueCode string

const (
	MissingRequiredField ConversionIssueCode = "MISSING_REQUIRED_FIELD"
	UndefinedReference   ConversionIssueCode = "UNDEFINED_REFERENCE"
	ValueOverridden      ConversionIssueCode = "VALUE_OVERRIDDEN"
	ValueDropped         ConversionIssueCode = "VALUE_DROPPED"
	ValueIgnored         ConversionIssueCode = "VALUE_IGNORED"
	ValueInvalid         ConversionIssueCode = "VALUE_INVALID"
	BehaviorMayDiffer    ConversionIssueCode = "BEHAVIOR_MAY_DIFFER"
	InternalError        ConversionIssueCode = "INTERNAL_ERROR"
)

type ConversionIssueSeverity string

const (
	Error   ConversionIssueSeverity = "ERROR"
	Warning ConversionIssueSeverity = "WARNING"
)

// ConversionIssueSource describes the source of an issue found during
// conversion.
type ConversionIssueSource struct {
	// Kind is the entity type that caused the issue, for example, "model" or
	// "agent". The stability contract lists every kind.
	Kind string

	// Name is the entity name that caused the issue, for example, "gpt-5".
	Name string

	// Fields are the paths of the fields that caused the issue. Each path is
	// relative to the entity, for example, ["targets[0].provider", "type"].
	Fields []string
}

// ConversionIssue describes one issue found during conversion.
type ConversionIssue struct {
	// Code identifies the class of the issue.
	Code ConversionIssueCode

	// Reason tells why the converter reported the issue.
	Reason string

	// Source is the entity that caused the issue.
	Source ConversionIssueSource

	// Severity is the level of the issue.
	Severity ConversionIssueSeverity
}
```

### Stability contract

Codes, severities, and `Source.Kind` values are public API. Callers can match
on them.

- Adding a code or a kind is not a breaking change.
- Callers must handle unknown codes by their `Severity`.
- Callers must accept unknown kinds.
- Renaming, removing, or reusing a code or a kind is a breaking change.
- Adding a severity is a breaking change. Callers use `Severity` to handle
  unknown codes. They must know every severity.

`Source.Kind` is the singular form of the top-level document key that holds the
entity:

| Kind | Document key |
|---|---|
| `model` | `models` |
| `model_provider` | `model_providers` |
| `mcp_server` | `mcp_servers` |
| `agent` | `agents` |
| `policy` | `policies` |
| `custom_policy` | `custom_policies` |
| `datastore` | `datastores` |
| `auth_strategy` | `auth_strategies` |
| `consumer` | `consumers` |
| `consumer_group` | `consumer_groups` |
| `vault` | `vaults` |
| `ca_certificate` | `ca_certificates` |
| `certificate` | `certificates` |
| `sni` | `snis` |

An issue in a nested object uses the kind of the top-level entity. For
example, an issue in a consumer credential has the kind `consumer`.

Each `Source.Fields` path uses the keys of the input document:

- A `.` separates the keys, for example `config.route.paths`.
- A list index uses `[n]`, starting at 0, for example `targets[0].provider`.
- A path is relative to the entity. It does not include the document key or the
  entity index.

`Fields` paths follow the input schema. A change to the input schema can
change a path. The converter does not change a path for any other reason.

`Reason` is not API. It can change in any release. Callers must not parse it.

The convert function returns this result:

```go
type ConversionResult struct {
	// Output is the Kong document that the conversion generates. It is never
	// nil. When every entity fails, Output is an empty document.
	Output *kong.Document

	// Metadata identifies the AI Gateway source of the generated entities.
	Metadata ConversionMetadata

	// Issues holds every issue found during conversion.
	// The caller decides how to handle them.
	Issues []ConversionIssue
}
```

### Behavioral changes

- This RFC removes `Options.Strict`.
- These current warnings become errors that leave out the entity:
  - an undefined auth strategy reference
  - `access.metadata` without `resource` or `authorization_servers`
  - an agent without `config.url`
- The converter emits these items today. It now leaves them out and reports a
  `warning`:
  - an MCP tool without `description`
  - a target with no resolvable provider type or an undefined provider. If it
    is the last target, the model is also left out, with an `error`.
- A current fatal error now leaves out only the affected entity. The run
  continues. For example:
  - an unsupported capability
  - an acl with both `allow` and `deny`
  - an auth strategy with `token_exchange`
  - an entity conflict

# Drawbacks
[drawbacks]: #drawbacks

This is a breaking change. The convert function now returns
`(*ConversionResult, error)`. `ConversionResult` contains the list of issues.
The function returns an `error` only when the whole run fails. Then
`ConversionResult` is nil. For example, the input is not valid YAML, an option
is not valid, or the converter cannot change the output to YAML.

Callers also have more work. Each caller inspects the issues and decides
which ones stop its workflow.

To collect every issue in one run, the converter must continue after an
`error` issue. Today each `failAt` call returns at once. Later steps depend on
earlier ones. Each `failAt` site therefore needs a path that skips the
failed entity and continues.

Shared routes add to this cost. Several models can share one route and one
route-scoped `ai-proxy-advanced` plugin. To leave out one of these models, the
converter removes its targets from the shared plugin. It cannot delete the
route.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

This design lets callers classify issues. Each caller decides which issues
stop its workflow. Other issues only notify the user. A clear classification
system lets callers make this decision without guessing.

An alternative approach is to return error messages as strings, as today. This
approach does not remove the need for callers to parse error messages. It also
does not give a clear way to classify errors.

Today a caller that handles errors must parse the field and the message of each
conversion error. The caller must know every string that a message can contain.
A change to a message can break existing callers.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

# Testing
[testing]: #testing

- Each golden test case records the expected issues in `issues.yaml`, next
  to `expected.yaml`.
- `revert/roundtrip_test.go` changes from "zero warnings" to "zero issues".
- New golden test cases cover the rules for partial output:
  - an entity whose auth strategy does not exist or cannot be converted is
    absent.
  - one bad nested item is absent. The rest of the entity converts. The
    `warning` has the path of the item.
  - a model whose targets are all left out is absent, with an `error`.
  - a global or referenced policy that cannot be converted gives an `error`.
    The rest of the configuration converts.
  - a panic in one entity gives an `INTERNAL_ERROR` issue. The other
    entities convert.
