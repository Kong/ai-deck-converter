---
description: Convert the intelligent balancer of a model
approvers:
  - team-koko
tags:
  - convert
  - models
  - api
jira: https://konghq.atlassian.net/browse/KOKO-XXXX
---

# Motivation
[motivation]: #motivation

AI Gateway 2.4 adds the `intelligent` balancer algorithm to `ai-proxy-advanced`.
For each request, a classifier model reads the request. The balancer then picks
the target that fits the operator's cost and quality preset.

The data plane takes numbers: `cost_quality_tradeoff` from 0 to 1, and a
`margin` in points of pass rate for the margin strategy. It also takes a
classifier record with a URL, a model name and one auth header. The Konnect API
shows the operator three presets and a reference to a classifier model. The
converter changes one shape to the other.

# Scope
[scope]: #scope

- In scope:
  - `config.balancer.algorithm: intelligent` on models, in both directions.
  - The preset table, the hidden strategy switch, and the classifier reference.
- Out of scope:
  - `config.balancer.session_affinity`. Kong/ai-deck-converter#309 changes it
    to `conversation_state` for every algorithm.
  - How the data plane picks a target. The weighted and margin algorithms own
    that.

# Guide-level explanation
[guide-level-explanation]: #guide-level-explanation

A model names a classifier model and a preset. The classifier model is an
ordinary model of the same document with the `decisions` capability.

```yaml
models:
  - name: task-classifier
    capabilities: [decisions]
    formats: [{type: typesafe}]
    targets:
      - name: jev-latest
        provider: typesafe-hosted
  - name: auto
    capabilities: [generate]
    config:
      balancer:
        algorithm: intelligent
        version: v1
        cost_quality_tradeoff: balanced
        classifier:
          model: task-classifier
          timeout: 5000
          context_window: 32768
    targets:
      - {name: claude-haiku-4-5, provider: anthropic}
      - {name: claude-opus-4-6, provider: anthropic}
```

The converter gives this balancer block:

```yaml
balancer:
  algorithm: intelligent
  cost_quality_tradeoff: 0.5
  strategy: weighted
  classifier:
    type: model
    provider: typesafe
    name: jev-latest
    auth: {header_name: Authorization, header_value: "{vault://env/typesafe-token}"}
    timeout: 5000
    context_window: 32768
```

A self-run classifier is the same model with `upstream_url` on its target, for
example `http://kev.internal:8009/v1/systemone`. The classifier record then has
`type: custom`, `url: http://kev.internal:8009` and `format: /v1/systemone`. A
host that serves System One at another path keeps that path. For example,
`https://openrouter.ai/api/alpha/decisions` gives `url: https://openrouter.ai`
and `format: /api/alpha/decisions`.

# Reference-level explanation
[reference-level-explanation]: #reference-level-explanation

## Presets

`internal/aimap/intelligent.go` holds the one table. Convert and revert both
read it.

| Preset     | `cost_quality_tradeoff` | `margin` (points of pass rate) |
|------------|-------------------------|--------------------------------|
| `cost`     | `0.25`                  | `15`                           |
| `balanced` | `0.5`                   | `5`                            |
| `quality`  | `0.75`                  | `0`                            |

`balanced` is the default. The tradeoff values are placeholders. The owner of
the weighted algorithm sets them.

The platform holds the table, not the data plane and not the operator:

- The table has one place to tune. A new value needs a converter release, not
  a data plane release.
- The operator picks a preset, not a number. The numbers can change under the
  same preset when we learn from real traffic.

The converter always writes `cost_quality_tradeoff`, because the data plane
requires it. It writes `margin` only for the margin strategy. A `margin` field
on the model fails the model. `version` has no data plane field. The converter
accepts only `v1` and writes nothing for it.

## Strategy

`strategy` is `weighted` (the default) or `margin`. The converter always writes
it. It is a hidden tech preview switch (`x-internal`). We remove it at GA, when
data chooses one algorithm.

## Classifier

`classifier.model` must name a model with the `decisions` capability. The
converter restricts the classifier by capability, not by provider type. The
converter copies that model's one target and its provider auth into the
classifier record. One option covers TypeSafe's hosted API and a classifier
the customer runs. The operator configures the provider, the auth and the URL
once, on a model they can also call and watch.

Today the gateway serves `decisions` only through `typesafe` providers (RFC
00230). When more provider types get the capability, the classifier follows
them with no converter change.

The model fails with an error at `config.balancer.classifier.model` when the
classifier model:

- does not exist, or is disabled;
- does not have the `decisions` capability;
- has no target or more than one target;
- has a target whose provider is unknown;
- has a target with no `upstream_url` and a provider type with no known
  default decisions endpoint. TypeSafe's hosted API is the only one today;
- has a provider with auth other than one header, such as query `params`, or
  with a token vault. The data plane sends one fixed header;
- has an `upstream_url` that the data plane rejects as a classifier URL: no
  `http` or `https`, no host, a query, a fragment, or user information.

A target with no `upstream_url` uses its provider type's default decisions
endpoint. For a `typesafe` target, the record is `type: model` with
`provider: typesafe`. A target with `upstream_url` gives `type: custom`.

A typesafe target sends to its `upstream_url` without a change. The data plane
joins the classifier `url` and `format` with one slash. Hosts serve System One
at different paths. The converter therefore splits `upstream_url`:

- `url` is `scheme://host[:port]`.
- `format` is the path, kept exactly, for example `/api/alpha/decisions`.
- An `upstream_url` with no path, or with only `/`, gets `format: /v1/systemone`.

## Revert

Revert changes each number back to its preset. A number that no preset has
gives the nearest preset and a warning. A tie goes to `balanced`. The margin
strategy reads `margin`, and the weighted strategy reads
`cost_quality_tradeoff`. Revert drops the default preset and the default
strategy.

Revert changes the classifier record back to `classifier.model`. It joins
`url` and `format` as the data plane does. It picks the first reverted model
with the `decisions` capability and one target of the same name and auth
header. For `type: model`, the target has the record's provider type and no
`upstream_url`. For `type: custom`, the target's `upstream_url` gives the same
joined URL. When no model
matches, revert warns and leaves out `classifier.model`.

## Version gate

`FeatureIntelligentBalancer` is `2.4`. Below it, a model with the intelligent
algorithm is absent with an `UNSUPPORTED_IN_MIN_RUNTIME_VERSION` error.

# Drawbacks
[drawbacks]: #drawbacks

- A classifier model is also a route on the gateway. An operator who wants
  only the classifier gets a callable `decisions` route too.
- One target only. A classifier with a failover target needs a later change.

# Rationale and alternatives
[rationale-and-alternatives]: #rationale-and-alternatives

- Numbers in the API, as the data plane takes them. The operator then tunes a
  value that only makes sense against the benchmark data, and every change of
  the table needs a new API version.
- A classifier record in the API with its own provider, URL and auth, as in
  the first draft of Kong/platform-api#3930. It duplicates the provider and
  model entities. It also needs two shapes, `model` and `custom`, where one
  reference covers both.

# Unresolved questions
[unresolved-questions]: #unresolved-questions

- The final tradeoff values of the weighted algorithm.
- The review outcome of Kong/platform-api#3930 and the data plane fields in
  kong-ee (`strategy`, `margin`, `classifier.timeout`,
  `classifier.context_window`).
