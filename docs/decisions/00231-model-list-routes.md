---
description: Model list routes for client model discovery
approvers:
  - team-koko
tags:
  - ai-gateway
  - models
  - discovery
jira: https://konghq.atlassian.net/browse/AIXSM-103
---

# Motivation
[motivation]: #motivation

A coding agent asks its gateway which models it may use, at `GET /v1/models`
under the address it calls. Kong AI Gateway has no such endpoint, so every
machine carries a hand-written model list that goes stale whenever an admin
adds or removes a model.

The data plane can answer that request. `ai-proxy-advanced` serves a model
catalog on a route when its config sets `route_type: llm/v1/models` and lists
the instances to advertise in `config.models.sources`. Nothing on the control
plane makes such a route, so the feature is unreachable from Konnect.

# Scope
[scope]: #scope

In scope:

- One list route per model base path, plus one at the gateway root.
- One source per `ai-proxy-advanced` instance a model emits, carrying the
  model's `access.acls` as the source access rule.
- The auth strategies of the listed models on the list route.
- The 2.3 runtime gate, and revert dropping list routes.

Out of scope:

- Creating list routes for a model whose base path already serves a `GET`
  route. The administrator is told instead; that is AIXSM-104.
- Evaluating the access rule at the data plane. The gateway owns that.

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

A model declared on `/claude-code` becomes a list at
`/claude-code/v1/models`. The gateway root also serves `/v1/models` with
every model, so a tool that knows only the gateway address still discovers
its models. A model appears on each list that covers it.

Each list advertises the `ai-proxy-advanced` instances of its models. The
gateway fills each entry from the instance targets, so the list shows the
alias, the display name, the context and output limits and the rates, without
a second source of truth.

A model's `access.acls` becomes the access rule of its list source. The
gateway hides a model from a caller the rule excludes, as the acl plugin
rejects that caller on the model route.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

`convertModelDirectories` runs at the end of a model pass. It groups each
model's emitted instance ids by base path and adds `aimap.DefaultBasePath` to
every model, so the root list covers everything.

Each group emits one route, `models` for the root and `models-<base>` for the
others, with `GET` on `aimap.RoutePath(base, aimap.ModelDirectorySpec)`, and
one route-scoped `ai-proxy-advanced` plugin:

```yaml
plugins:
  - name: ai-proxy-advanced
    route: models-team-a
    config:
      route_type: llm/v1/models
      models:
        sources:
          - plugin_id: c32e7c3e-b43c-56b9-bf90-d660df5cd572
            access: {deny: [contractors]}
```

`sources[].plugin_id` needs an id that survives a reload, so each
`ai-proxy-advanced` instance carries an `id` derived from its route name and
rendered config (`kong.StableUUID`). The rendered config decides it, not the
model name or alias, because revert recovers those differently and the round
trip must stay byte-identical.

The list route carries the union of the listed models' auth strategies, with
the same anonymous consumer and `request-termination` the model routes use. A
caller that matches no strategy reaches the anonymous consumer, which answers
401.

A model whose `access.acls` sets more than one of `allow`, `deny`,
`allow_when` and `deny_when` never reaches this step: its acl plugin fails, so
the model fails first.

A list route matches on its path only. A model route also matches on hosts,
SNIs and headers, so a caller those rules exclude can still read the list.
The list is a discovery surface, not an inference surface.

Two models that share one `type: api` instance share one `ai-proxy-advanced`
plugin, which carries one access rule. When their rules differ, the instance
is left out of every list with a warning, so the list never shows a model to a
caller its rule excludes.

The video lifecycle route of a video model has its own `ai-proxy-advanced`
instance and is not advertised: it serves a lifecycle operation rather than a
model a client selects.

A realtime (`ws`/`wss`) instance is not advertised either. Its targets carry
no `model_alias`, so the list has no id to show for them, and a plain HTTP
caller cannot reach a WebSocket-only route.

Below the 2.3 gate no list route is made, no instance gets an `id`, and one
`UNSUPPORTED_IN_MIN_RUNTIME_VERSION` warning names the conversion option.

Revert drops a route whose plugins carry `route_type: llm/v1/models`. The list
route has no AI Gateway entity, so it must not become a model.

`aimap.ModelDirectoryRouteType` and `aimap.ModelDirectorySpec` hold the
constants, shared by both directions.

# Drawbacks
[drawbacks]: #drawbacks

- On 2.3 and later, every advertised `ai-proxy-advanced` instance carries an
  `id`. The field is valid in decK and in db-less output, but it widens the
  diff for those configs. Below the gate no id is set, so the output for an
  older runtime stays as it was.
- A model declared on several base paths is advertised on several lists, so
  the same instance id repeats. The gateway deduplicates per caller.
- Two models on one base path with different auth strategies share one route
  and therefore one union of strategies. A caller authenticated by one
  strategy can discover a model the other strategy guards, unless the model
  also sets an access rule.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

- Generate the lists in Konnect instead of the converter. Rejected: write-time
  validation runs the same converter, so a second implementation could accept
  a config the data plane rejects.
- Reference the source instances by name. Rejected: the gateway schema accepts
  a plugin id only.
- Derive the source id from the model name and alias. Rejected: revert
  reconstructs the model differently, which breaks the byte-identical round
  trip for every multi-alias case.
- Put the model's ACL plugins on the list route. Rejected: the gateway reads a
  source's rule per model, so a route-scoped rule would hide or show the whole
  list.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

- What should happen when a base path already serves a `GET` route? The
  routing conflict warning reports it today; AIXSM-104 decides the rest.
- Should a list route be suppressed for a base path whose models disagree on
  auth strategies, rather than merging the strategies?
