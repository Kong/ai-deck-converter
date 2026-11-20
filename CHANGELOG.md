# Table of Contents

- [v0.0.1](#v001)

## [v0.0.1]

> Release date: TBD

### Features

- `budget_status: {enabled: true}` adds the budget status endpoint. The output is
  a `budget-status` Service with one `GET /budgets` Route and an
  `ai-proxy-advanced` plugin with `route_type: llm/v1/budgets`. The Route
  authenticates with the auth strategies of all converted models. A
  `BEHAVIOR_MAY_DIFFER` warning flags an endpoint without authentication,
  because the data plane answers 401 then. `options.timeout`,
  `options.document` and `options.resource` set the plugin's `budget_status`
  record. A `resource` also adds `/.well-known/budget-status` to the Route.
  Requires AI Gateway 2.4+. Revert lifts the Route and its options back.

### Bug Fixes

- A Bedrock target with `allow_override: true` and no header now gets
  `header_name: Authorization` and a placeholder `header_value`. The data plane
  rejected the whole configuration without them. This affected every Bedrock
  provider with `basic` auth and no header, which the converter marks as client
  auth since #304. A request without a client key still uses IAM auth.

- Model-scoped `response-transformer` and `response-transformer-advanced` policies
  now change the response body. `ai-proxy-advanced` wrote the body after them and
  discarded the change, so only header changes reached the client. The converter
  emits these policies as cloned plugins (`cloned_plugins`, priority 766) that run
  after `ai-proxy-advanced`. The output now carries `cloned_plugins`, which needs a
  paid license and a decK that supports plugin definitions
  (`--include-plugin-definitions`). Requires AI Gateway 2.1+; older releases,
  global policies and consumer- or consumer-group-scoped policies get a
  `BEHAVIOR_MAY_DIFFER` warning, as does a plain `response-transformer`
  transforming the body for a gzip-accepting client.
