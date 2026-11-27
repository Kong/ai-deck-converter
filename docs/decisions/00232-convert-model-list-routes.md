---
description: Generate model discovery routes in ai-deck-converter
approvers:
  - team-koko
tags:
  - convert
  - routing
  - discovery
jira: https://konghq.atlassian.net/browse/AIXSM-103
---

# Motivation
[motivation]: #motivation

Claude Code needs a list of the models it can use through AI Gateway. The
`ai-models-list` plugin in AI Gateway 2.4 serves that list in the format Claude
Code expects. The converter will set up the routes and plugin from the model
config so users can stop keeping lists by hand.

# Scope
[scope]: #scope

- Add Anthropic and OpenAI model lists to decK and DB-less output.
- Keep the model routes' match rules and auth. Carry over each model's access
  rule and protect existing agent routes that handle `GET` on a list path.
- Turn lists on from minimum runtime version 2.4. Let users hide models.
  Handle generated routes and hidden models in `revert`.

The shared RFC and data-plane design cover the plugin. The Platform API handles
its own schema change. Other formats, OpenAI-format Bedrock Mantle lists, extra
fields such as prices and token limits, and a gateway-wide switch are separate
work.
[Shared model-list RFC][shared-rfc] · [Data-plane design][dp-erd]

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

Use the same base URL for model calls and lists. Anthropic clients call
`GET <base>/v1/models`. OpenAI clients call `GET <base>/models`. A model appears
on each list for its base paths and formats. With no base path, it appears at
`/`. A root list covers only models on `/`.

Each alias in `config.route.model.values` gets an entry, even if it serves
several upstream targets. With no aliases set, use the model name. Set
`config.model.listed: false` to hide a model from lists and keep it available
for model calls.

| Models | Base path | List path | Auth |
|---|---|---|---|
| Anthropic `claude-sonnet` and `claude-opus` with alias `claude-opus-4-7` | `/claude-code` | `/claude-code/v1/models` | `openid-connect` |
| OpenAI `gpt-team` | `/` | `/models` | `key-auth` |
| OpenAI `gpt-public` | `/open` | `/open/models` | Public |
| Passthrough `openai-raw` | `/raw` | Provider's own list | Passthrough route auth |

If `claude-opus` denies the `contractors` group, a contractor who signs in sees
only `claude-sonnet`. On a public route, callers without credentials see only
entries with no access rule.

If an enabled agent already serves `GET /claude-code/v1/models` for the same
requests, the converter keeps the agent and warns. Hiding both Anthropic
models clears the warning.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

## Which models to list

Add `Listed *bool` to `ModelNameConfig` in `internal/aigw/model.go`. Use `listed`
as the YAML key. An unset field means `true`. Add `FeatureModelLists` at 2.4 in
`internal/aimap/versions.go`. Below 2.4, skip all lists. If a converted model
sets either value, warn with `UNSUPPORTED_IN_MIN_RUNTIME_VERSION` at
`config.model.listed`. The Platform API will use the same version check.
[Runtime compatibility RFC][versions]

In `convert/model.go`, keep the source model, base path, resolved format, HTTP
match rules, and aliases from the final successful model pass. Use the existing
rules for aliases and wildcards. Skipped models and failed targets add no
entries.

List only aliases clients can select in `openai` or `anthropic`. Skip
passthrough, `type: api`, Gemini Interactions, realtime, the Responses and video
lifecycle routes, and OpenAI-format Bedrock Mantle. Anthropic-format Mantle
uses the usual Anthropic list path if it meets the same rules.

From 2.4, accept `listed: false` without a warning on a model with no listable
routes. There are no entries to hide. `revert` leaves the field unset for
these models.

## Route generation

Add `convertModelLists` in `convert/model_list.go`. Run it just before `lint`,
after all other routes exist. Models share a list when all three match.

- The list path from `aimap.RoutePath`. `/team` and `/team/` give the same path.
- The HTTP `protocols`, `hosts`, `headers`, `snis`, `sources`, and `destinations`
  used by the model routes. An HTTPS-only model gets an HTTPS-only list.
- The set of auth strategies from `authStrategyKey`.

For each group with entries, add a route under `ai-gateway` with its list path
and `methods: [GET, OPTIONS]`. The shared RFC shows `GET` alone. The converter
follows the data-plane design and includes `OPTIONS` for CORS preflights.
An OpenAI model on `/v1` and an Anthropic model on `/` can share `/v1/models`.
Each entry carries its own format. [Shared model-list RFC][shared-rfc] ·
[Data-plane design][dp-erd]

Use `<format>-models` at the root and `<format>-models-<base>` elsewhere. Get
the base by removing the format's suffix from the path `aimap.RoutePath` made.
Replace characters Kong rejects. This rule also covers regex base paths.
Mixed formats use the OpenAI base, so the shared route above is `openai-models-v1`
in either model order. Check all output route names before adding a number.
Keep the order of routes and entries stable. Use the models in each group as
sources for routing warnings.

## Plugins and access rules

Add one `ai-models-list` plugin per list route. Write one `config.models` entry
per model, alias, and format. Set `name` to the alias and `format` to the resolved
format. Copy `allow`, `deny`, `allow_when`, or `deny_when` from `access.acls`
into `access`. Leave out `access` when there is no rule. Models that share an
alias keep their own entries and rules. [AIXSM-105][model-access]

The decK example shows the list route and its plugins. It leaves out the OIDC
settings.

```yaml
services:
  - name: ai-gateway
    url: http://ai-gateway.upstream.local
    routes:
      - name: anthropic-models-claude-code
        paths: [/claude-code/v1/models]
        methods: [GET, OPTIONS]
plugins:
  - name: openid-connect
    route: anthropic-models-claude-code
  - name: ai-models-list
    route: anthropic-models-claude-code
    config:
      models:
        - name: claude-sonnet
          format: anthropic
        - name: claude-opus-4-7
          format: anthropic
          access: {deny: [contractors]}
```

Reuse the model routes' auth plugins, including the anonymous consumer and
`request-termination` fallback where needed. With no strategies, add just the
list plugin to the route. Global policies still apply. Groups with the same
match rules and different auth strategies get separate routes. The routing
check reports their overlap as `ROUTING_CONFLICT`.

The converter depends on three plugin rules.

- Run after auth and `request-termination`. The plugin priority is `1`, below
  `request-termination` at `2`. The fallback can then return `401` before the
  list plugin runs.
- Show only entries for the request's format on a shared route.
- Use the same groups as the model route's `acl` plugin, including consumer
  groups and groups from auth. On a public route, a caller without credentials
  sees only entries without access rules.

[Data-plane design][dp-erd]

For models in a list, scan CEL rules for `http.`, `route.`, and `service.`. On a
match, warn once per model with `BEHAVIOR_MAY_DIFFER` at `access.acls`. A caller
who signs in can see the alias even if the rule blocks the model call. The
text check can also match a string literal.

## Existing agent routes

Compare a `GET`-only copy of the list route with each enabled agent. Reuse
the non-path checks from `routesOverlap` in `convert/lint.go`. An agent with
no methods set matches `GET`.

- A plain agent path covers the list path if it is equal to it or is a prefix
  of it under Kong's path rules. An agent with no paths covers every path.
  For example, `/claude-code` covers `/claude-code/v1/models`.
- If either path is regex and the other match rules overlap, assume the paths
  overlap. The converter does not try to compare regex matches.
- For overlapping routes, keep both only when `refines(agent, list)` holds
  with the full method sets. For example, an agent on the same path with an
  extra host rule and `methods: [GET]` can keep that host while the list serves
  others.

For all other `GET` overlaps, skip the list and warn with `ROUTING_CONFLICT` at
the agent's `config.route`. Name the list path. Keep the usual routing check
for all output routes, including `OPTIONS`. Its path rules stay as defined in
RFC 00229. [Routing conflict RFC][conflicts] · [AIXSM-104][agent-conflicts] ·
[Kong path matching][kong-paths]

## Revert and output modes

Share the list plugin name in `internal/aimap`. In `revert/service.go`, skip
routes with that route-scoped plugin and skip their other route plugins. Check
before treating the route as a model or agent. Use the existing DB-less writer
for the new routes and plugin references.

From 2.4, add a reserved tag to each listable `ai-proxy-advanced` instance of a
hidden model, even if the whole group is hidden. Tag the plugin so models that
share an `ai_models` entry can have different settings.

In `revert`, use the tag to restore `listed: false`. Keep tagged and untagged
listable instances in separate model groups even if they share an alias. Ignore
model routes that cannot appear on a list. A missing list can mean an agent
owns the path. Keep the warning when two plugins share a model reference on one
route.

## Implementation and verification

1. Add the model field and version check. Collect list entries during model
   conversion, then add route and plugin output, warnings, and `revert` support.
2. Check decK and DB-less output. Refresh the default-version golden files
   and add a case with `min_runtime_version: 2.4`. Cover the root and multiple
   paths, route name clashes, trailing slashes, and both orders of a shared
   format path. Test default and explicit aliases, shared aliases with
   different ACLs, and hidden models.
3. Test public routes, global auth, key auth, OIDC, and multiple strategies.
   Cover HTTPS-only routes and host and header rules. Test exact, prefix, and
   regex agent paths, including a regex agent on a different host. Cover
   disabled agents, unset methods, `OPTIONS` only, and both sides of the
   refinement check. Below 2.4, expect a warning only for an explicit
   `listed` value and no lists. Check CEL warnings for rules that read the
   request and no warnings for rules that only read the caller.
4. Run `TestRoundTrip` with all models listed, some hidden, and all hidden.
   Cover a shared alias with different settings across endpoints, and a
   model with both listable and skipped endpoints. Check each route kind
   excluded above, including paths with passthrough alone or mixed with
   listable models.
5. On a 2.4 image with the plugin, test both client formats on `/v1/models`,
   host isolation, OIDC claim groups, and a public list. Expect `401` with
   no valid credential on key-auth and multi-strategy routes. Check that a
   hidden model still serves calls, a passthrough-only path reaches the
   provider, and a prefix agent still answers `GET`. Test global CORS and
   a global rate limit on the list route.

# Drawbacks
[drawbacks]: #drawbacks

- Moving the minimum runtime to 2.4 adds routes by default. Users can hide
  models one at a time.
- Routes with the same match rules and different auth compete for one
  request. Give them different match rules to avoid the conflict.
- An ACL that reads the request can let a caller see an alias they cannot use.
  Hide the model to keep that alias off the list.
- With both passthrough and listable models on a path, the gateway list can
  take the place of the provider list at that path.
- A regex agent or list path can cause the converter to skip a list even if
  the paths match different requests.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

- Build routes in the converter so validation and deployment use the same
  output. Doing this in Konnect would repeat the routing rules.
- Group by route rules and auth. A path-only group or a gateway-wide root list
  can expose private aliases or join auth plugins that do not work together.
- Build entries from model settings to avoid a link to generated plugin IDs.
  An ACL on the route would apply one model's rule to the whole list.
- Use a model setting so users can hide one model at a time. A converter-wide
  switch applies to every model. A `models` capability would tie a shared list
  to one upstream provider.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

- If an agent handles only `OPTIONS` and its route overlaps the list without
  a clear winner, should the converter skip the list? This draft keeps the list
  and warns about the conflict.

[shared-rfc]: https://github.com/Kong/aigw-cpdp-contracts/blob/main/releases/2.4/model-list-routes/rfc.md
[dp-erd]: https://docs.google.com/document/d/1IdjqSfC13HdHi7_HPO8KpizpeCJI7-1w3s1jZB7xSko/edit
[versions]: 00225-dataplane-conversion-compatability.md
[conflicts]: 00229-routing-conflict-detection.md
[agent-conflicts]: https://konghq.atlassian.net/browse/AIXSM-104
[model-access]: https://konghq.atlassian.net/browse/AIXSM-105
[kong-paths]: https://developer.konghq.com/gateway/routing/traditional/#path
