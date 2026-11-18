# Table of Contents

- [v0.0.1](#v001)

## [v0.0.1]

> Release date: TBD

### Features

- Models convert the `intelligent` balancer (AI Gateway 2.4+). A
  `cost_quality_tradeoff` preset becomes the data plane number, and
  `classifier.model` names a model with the `decisions` capability, whose
  one target becomes the classifier.

### Bug Fixes

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
