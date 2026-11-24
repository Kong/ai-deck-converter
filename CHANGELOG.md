# Table of Contents

- [v0.0.1](#v001)

## [v0.0.1]

> Release date: TBD

### Features

- A model's `config.balancer.session_affinity` block now becomes the
  `ai-proxy-advanced` `conversation_state` block, so a conversation sticks to the
  target that served its first turn. The block needs AI Gateway 2.4+; an older
  release reports `UNSUPPORTED_IN_MIN_RUNTIME_VERSION` and leaves the model out.
  `session_affinity` on the `consistent-hashing` balancer is rejected, because that
  algorithm has no affinity layer. A model's `datastores` list now carries a
  `purpose` per entry (`semantic-balancer`, the default, or `session-affinity`), and
  a `session-affinity` datastore replaces the inline Redis connection. A list with
  one entry and no purpose converts as before.

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
