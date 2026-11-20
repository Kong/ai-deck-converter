# Table of Contents

- [v0.0.1](#v001)

## [v0.0.1]

> Release date: TBD

### Features

- A model that needs a WebSocket route and targets a provider whose credential
  comes from a Token Vault converts at AI Gateway 2.4 and later. The
  `token-vault-exchange` plugin now carries the `ws` and `wss` protocols of
  its route. Below 2.4 the model fails with an `UNSUPPORTED_IN_MIN_RUNTIME_VERSION`
  issue, as before. The gateway applies only the `header` and `query`
  credential placements on a WebSocket route, so a provider that reads or
  sends the vault credential from a `body` placement fails with a
  `CONFIG_FIELD_INVALID` issue at 2.4 and later.

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
