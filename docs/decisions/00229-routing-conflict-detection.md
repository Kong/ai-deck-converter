---
description: Detect and report routing conflicts between models, MCP servers, and agents
approvers:
  - team-koko
tags:
  - convert
  - routing
  - api
jira: https://konghq.atlassian.net/browse/KOKO-4720
---

# Motivation
[motivation]: #motivation

An AI Gateway control plane does not detect routing conflicts today. A user
can configure models, MCP servers, and agents that listen to the same
requests. Kong then sends each such request to only one route. The other
entity does not get the request. The converter reports no issue for the 
example conflicts below.

Two layers decide where a request goes:

- **Route matching.** Kong selects one route from its matchers: `paths`,
  `hosts`, `methods`, `protocols`, `headers`, `snis`, `sources`, and
  `destinations`.
- **Model matching.** On a shared model route, `ai-model-selector` selects one
  `ai_models` entry. It reads the model name from the request body, a header,
  or the path.

A conflict on either layer makes routing ambiguous. Examples:

- Two MCP servers on the path `/mcp/secure-flights`.
- Two MCP servers with no paths. Each one matches every request.
- Two models on one base path with different auth strategies. The converter
  splits them into two routes with equal paths. Kong selects one route, and
  the models on the other route are not reachable.
- Two models with the same alias. The models share one `ai_models` entry.

This RFC makes the converter report each of these conflicts.

# Scope
[scope]: #scope

- In scope:
  - Route conflicts between any two routes that the converter makes: model
    routes, video lifecycle routes, MCP server routes, and agent routes.
  - Model name conflicts: two models with the same alias.
  - Model selector conflicts: two models on one shared route that read the
    model name from different places.
  - A new issue code, `ROUTING_CONFLICT`.
  - The code of two existing routing conflict checks. See
    [Existing routing conflict checks](#existing-routing-conflict-checks).
  - Omitting disabled models from output. See [Disabled models](#disabled-models).
- Out of scope:
  - Validating against a copy of the Kong router priority rules.
  - Comparison of regex values (paths, headers, etc.) with plain values.
  - Port-aware comparison of hosts, for example `example.com` with
    `example.com:8080`. See [Hosts](#hosts).
  - Modifying conversion output when route and selector conflicts are detected.
  - The revert flow.

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

The converter will "lint" the generated document and surface issues
to the caller via the mechanism defined in [00207-conversion-issue-reporting](00207-conversion-issue-reporting.md).
The reported issues will have type `ROUTING_CONFLICT`, where the issue source
is the later entity in the document and the reason names the other entity.

Furthermore, it is up to the caller to decide how to handle `ROUTING_CONFLICT` issues. 
For example, an API may choose to handle these issues by responding with a BAD REQUEST response 
whereas another tool may choose to ignore them.

New concepts:
- **Routing conflict**: two entities can receive the same request, and the
  data plane selects one of them without a rule that the user controls.
- **Overlap**: two values of one matcher can match one request.

### Example 1: two MCP servers on one path

```yaml
mcp_servers:
  - name: flights-oauth
    config: {route: {paths: [/mcp/flights]}}
  - name: flights-consumer
    config: {route: {paths: [/mcp/flights]}}
```

```yaml
- code: ROUTING_CONFLICT
  severity: WARNING
  source: {kind: mcp_server, name: flights-consumer, fields: [config.route]}
```

### Example 2: an unset matcher

```yaml
mcp_servers:
  - name: flights-mcp
    config: {route: {paths: [/mcp/flights]}}
agents:
  - name: flights-writer
    config: {route: {paths: [/mcp/flights], methods: [PUT]}}
```

`flights-mcp` sets no methods, so it also matches `PUT`. The converter reports
a conflict on `flights-writer`. An agent on the same path with
`methods: [GET]` does not conflict with `flights-writer`.

### Example 3: a route split by auth strategies

```yaml
models:
  - name: open
    capabilities: [generate]
    config: {route: {paths: [/shared]}}
  - name: secured
    capabilities: [generate]
    access: {auth_strategies: [keys]}
    config: {route: {paths: [/shared]}}
```

Models with different auth strategies cannot share a route. The converter
makes `openai-chat` and `openai-chat-2`, both on `/shared/chat/completions`.
It reports a conflict on `secured`.

### Example 4: one alias on two models

```yaml
models:
  - name: a
    config: {route: {model: {values: [gpt]}}}
  - name: b
    config: {route: {model: {values: [gpt]}}}
```

```yaml
- code: ROUTING_CONFLICT
  severity: WARNING
  source: {kind: model, name: b, fields: [config.route.model.values]}
```

The converter keeps `b`. A model without `values` uses its name as the
alias. The field is then `name`.

### Example 5: different selector sources on one route

```yaml
models:
  - name: a
    config: {route: {paths: [/shared]}}
  - name: b
    config:
      route:
        paths: [/shared]
        model: {header: {header_param: x-model}}
```

Both models share one route. `a` reads the model name from the body. `b` reads
it from the `x-model` header. A request can name a model in both places. The
order of `config.sources` in `ai-model-selector` then decides the model. The
converter reports a conflict on `b` with the field `config.route.model`. It
reports no route conflict, because the two models share one Kong route.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

## Issue code

`convert/issue.go` adds this code:

```go
RoutingConflict ConversionIssueCode = "ROUTING_CONFLICT"
```

RFC 00207 says that a new code is not a breaking change. A caller that does
not know the code handles it by its severity.

## Route conflicts

As a last step, `Converter.run` calls `warnRoutingConflicts` with the
generated output document. It collects the routes of every enabled service
and compares each pair of routes. For a pair that overlaps, it reports a
conflict on the later route. Models that share one route are not a pair.
`ai-model-selector` separates them, and a model selector conflict covers them
instead.

Two routes overlap when every matcher overlaps:

| Matcher                   | Unset value     | Two values overlap when                                |
|---------------------------|-----------------|--------------------------------------------------------|
| `protocols`               | `http`, `https` | they are equal, without case                           |
| `methods`                 | every method    | they are equal, without case                           |
| `paths`                   | every path      | they are equal                                         |
| `hosts`, `snis`           | every host      | one host name matches both patterns                    |
| `headers`                 | every header    | see below                                              |
| `sources`, `destinations` | every address   | the IP ranges overlap and the ports are equal or unset |

Two lists overlap when one list is empty, or when one value of each list
overlaps. `protocols` is different: an empty list is Kong's default, not every
protocol.

### A matcher that only one route sets

When one route sets a matcher and the other route leaves it unset, the
matcher overlaps. The converter reports a conflict with the `WARNING`
severity.

Kong prefers the route with more matchers, so it can order such routes. The
converter still reports the conflict. The less specific route loses every
request that the more specific route matches, and the user does not see this
in the configuration. A `WARNING` tells the user and keeps both entities.

For example, route A has `paths: [/shared]`. Route B has `paths: [/shared]`
and `hosts: [other.example.com]`. Requests to `other.example.com/shared` go
to B and never reach A.

### Paths

Only equal paths overlap. Kong prefers the longer of two prefix paths, so
`/a` and `/a/b` do not make a conflict. A regex path overlaps only an equal
regex path. A regex path and a plain path never overlap.

### Hosts

Kong allows one wildcard at the start or at the end of a host pattern:

- Two plain hosts overlap when they are equal, without case.
- A wildcard pattern and a plain host overlap when the pattern matches the
  host. `*.example.com` matches `api.example.com`. It does not match
  `example.com`.
- Two leading wildcards overlap when one suffix, with its leading dot, ends
  with the other. `*.xexample.com` and `*.example.com` do not overlap.
- Two trailing wildcards overlap when one prefix, with its trailing dot,
  starts with the other. `a.*` and `ab.*` do not overlap.
- A leading and a trailing wildcard always overlap. `example.com` matches
  both `*.com` and `example.*`.

A host with a port is compared as one string. `example.com:8080` overlaps
only `example.com:8080`, not `example.com`.

`snis` use the same rules as `hosts`. Kong uses `snis` only on routes with a
TLS protocol, for example `https` or `tls`. The check needs no extra rule for
this: two routes overlap only when their protocols also overlap.

### Headers

Header names are compared without case. A header that only one route sets
does not limit the other route, because one request can carry both headers.
For a header name that both routes set, at least one value of each route must
overlap:

- Two plain values overlap when they are equal, without case.
- A regex value (`~` or `~*` prefix) overlaps only an equal regex value.
- A regex value and a plain value never overlap.

Two lists of plain values that share no value do not overlap, and the check
reports no conflict. This misses one case. See [Drawbacks](#drawbacks).

### Issue source

A route carries a `Source` with the entity type and name. The issue uses
these as `kind` and `name`, with the field `config.route`. A model route that
several models share reports one issue per model on the shared route.

The same pair is reported one time. `Converter.report` already drops an
issue that is equal to a recorded issue.

## Model name conflicts

The data plane requires a unique `name` and `alias` for each `ai_models`
entry. The converter writes each model alias into `name` and leaves `alias`
unset, so one alias check covers both fields. `ToDBLess` gives equal names one
ID, so the data plane loads them as one entry.

The model pass maps each alias to the first model that uses it. A later model
with the same alias gets a `WARNING` and stays in the output. Each model scoped
plugin also has a route scope. A model on another route does not get it. The check covers only enabled models, because the converter leaves
out disabled models. See [Disabled models](#disabled-models).

## Disabled models

Today the converter leaves out a disabled `type: api` model. A disabled
`type: model` model, passthrough or not, still makes its routes, its
`ai_models` entries, and its plugins with `enabled: false`.

This RFC makes the converter leave out every disabled model. A model with
`enabled: false` makes no route, no `ai_models` entry, no plugin, and no
video lifecycle route. The converter reports no issue, because the user asked
for this result.

A disabled model in the output causes routing conflicts:

- Its `ai_models` entries keep its aliases. An enabled model with the same
  alias then makes two entries with one name, and the data plane rejects the
  configuration.
- Its routes still exist and still match requests. A disabled
  `ai-proxy-advanced` does not proxy them to a model, so an enabled entity
  with an overlapping route loses those requests.

When disabled models make no output, the model name check and the route check
need to look only at enabled models. A disabled MCP server or agent keeps its
service with `enabled: false`. The route check skips disabled services, so
these entities need no change.

This change is visible to callers:

- The output of each configuration with a disabled `type: model` model
  changes. A caller that reads the `ai_models` entry or the plugins of a
  disabled model no longer finds them.
- The reverse conversion cannot recover a disabled model, because the output
  has no trace of it. The reverse conversion does not recover `enabled: false`
  for models today either, so no round trip changes.
- The golden case `29_dbless_disabled_model_plugins` changes. Its only model
  is disabled, so its expected output has no model entities.

## Model selector conflicts

A selector shape is the selector config without `max_request_body_size` and
`pcre_pattern`. It identifies where the selector reads the model name. A model
gets a `WARNING` when a shared route already has a shape of another model that
this model does not use. Each pair of models is reported one time. WebSocket
and passthrough routes have no `ai-model-selector`, so they never get this
conflict.

## Existing routing conflict checks

The converter already reports two routing conflicts with other codes. This
RFC changes both to `ROUTING_CONFLICT`, so a caller finds every routing
conflict by one code. The severity and the output stay the same.

| Check                                    | Today                            | With this RFC                 |
|------------------------------------------|----------------------------------|-------------------------------|
| Passthrough models on one route path     | `CONFIG_FIELD_INVALID`, `ERROR`  | `ROUTING_CONFLICT`, `ERROR`   |
| Video lifecycle routes that models share | `BEHAVIOR_MAY_DIFFER`, `WARNING` | `ROUTING_CONFLICT`, `WARNING` |

### Passthrough models on one route path

A passthrough route has no `ai-model-selector`. Two passthrough models whose
routes match the same requests therefore cannot both work. `convertModel`
leaves out the later model with an `ERROR` on `config.route.paths`. This
 stays because it follows the rule for model name conflicts: the output
cannot work, so the later model is left out.

The route conflict check compares the request matchers of the two routes for exact
equality. The route check later sees only the earlier model, so it reports
no second issue for the pair.

### Video lifecycle routes that models share

Two OpenAI video models with the same route configuration each make a video
lifecycle route on the same paths. `videoLifecycleCandidates` reports a
`WARNING` on `config.route` of each of these models. Both models still
convert.

The route check also sees these overlapping lifecycle routes. It does not
report them again. The lifecycle check already names every model on the
paths, so a second issue for the same pair adds nothing.

The lifecycle check also reports a `BEHAVIOR_MAY_DIFFER` `WARNING` when one
lifecycle route has more than one target. This is not a conflict between two
entities, so its code stays.

### Compatibility

RFC 00207 makes codes public API. It calls renaming, removing, or reusing a
code a breaking change. This RFC does none of these. `CONFIG_FIELD_INVALID`
and `BEHAVIOR_MAY_DIFFER` stay, and other checks still use them. A caller that
matches on the old code for these two checks must match on
`ROUTING_CONFLICT`. A caller that handles issues by severity sees no change.

## Order

The checks run in conversion order. A model left out for a name conflict
therefore gets no route warnings. The output, the issues, and their order are
deterministic for one input.

# Drawbacks
[drawbacks]: #drawbacks

- **Warnings for intended configurations.** A user can add a more specific
  route, e.g., with `hosts` or `headers`, on top of a route without them. Kong
  prefers the more specific route, so the result can be what the user wants.
  The converter still reports a conflict because the less specific route
  loses those requests.
- **Missed conflicts.** Regex paths and regex header values are compared only
  by equality. `~/v1/.*` and `/v1/chat` can match one request, and the
  converter does not report it. Two different regex values can also match one
  request, for example `~/v1/.*` and `~/v1/chat$`. The converter does not
  report this either.
- **Missed repeated headers.** One request can carry the same header name
  twice, for example `X-Tenant: a` and `X-Tenant: b`. That request can match
  a route with `X-Tenant: [a]` and a route with `X-Tenant: [b]`. The two
  routes share no header value, so the converter reports no conflict.
- **Disabled models leave the output.** A caller that relies on the
  `ai_models` entry or the plugins of a disabled model loses them. See
  [Disabled models](#disabled-models).
- **Decreased conversion performance.** The route check compares each pair of
  routes, so it can be slow for large documents.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

Without this change, a user finds a routing conflict only when requests reach
the wrong entity at run time.

This RFC makes these choices:

- **Check the output, not the source.** Only the final output shows the
  generated routes and paths.
- **Use the severity that matches the effect.** A model name conflict makes
  the data plane reject the whole output. It therefore gets an `ERROR`, and
  the converter leaves out the later model so the rest of the output stays
  valid. Route and selector conflicts can be intended. They get a `WARNING`,
  and both entities still convert.

This RFC rejects these alternatives:

- **Validate against the Kong router priority rules.** Users don't have
  visibility into the Kong router priority rules, and are only in control of
  the input config that they provide.
- **Use `BEHAVIOR_MAY_DIFFER`.** A specific issue code makes it easier for
  users to identify and fix issues, and `BEHAVIOR_MAY_DIFFER` is too generic.
- **Keep the old codes for the existing checks.** Callers would then need
  three codes to find every routing conflict. One code is easier to match.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

None.

# Testing
[testing]: #testing

- Add unit tests in `convert/routing_conflict_test.go` that cover each
  matcher, the host wildcard rules, and disabled services.
- Add a golden test case that covers:
  - two models with one alias. The later model is left out with an `ERROR`.
  - two models with different selector sources on one route.
  - a route split by auth strategies.
  - a route with `hosts` and a route without `hosts`.
  - a model route that two models share and that overlaps another route. Each
    model on the shared route gets one issue.
  - an agent and an MCP server on one path, with and without `methods`.
  - two agents with different methods, which do not conflict.
- Update the golden case `29_dbless_disabled_model_plugins`, and add a
  disabled model that shares an alias and a route with an enabled model. Its
  output has only the enabled model, with no issue.
- Update the existing tests of the passthrough path check in
  `convert/model_test.go` and of the shared video lifecycle route in
  `convert/convert_test.go` to expect `ROUTING_CONFLICT`. Check that the
  route check reports no second issue for a shared lifecycle route.
- The existing golden cases `08_mcp_auth_proxy_upstream` and
  `64_mcp_token_vault` have a real conflict. Give their MCP servers distinct
  paths, so they stay free of issues.