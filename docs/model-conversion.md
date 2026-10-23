# Converting an Entity Type — worked example: the Model entity

This document describes **all the steps required to convert one AI Gateway
entity type into Kong decK entities**, using the **Model** entity as the
worked example. It is meant as a template: when you add or change a type
conversion, this is the shape of work involved and where each piece lives.

Everything starts in `Converter.run()` (`convert/convert.go`). Each entity
type gets its own `convert<Type>()` method, called in a fixed order. The Model
step is `convertModels()` in `convert/model.go` — the most intricate path, and
therefore the most useful to study.

```
Converter.run()                         convert/convert.go
  ├─ buildRegistries()                  index providers/policies/groups by name
  ├─ convertGlobalPolicies()
  ├─ convertVaults()
  ├─ convertConsumerGroups()
  ├─ convertConsumers()
  ├─ convertModels()   ◄── this guide   convert/model.go
  ├─ convertMCPServers()
  └─ convertAgents()
```

---

## 0. Know your two ends

A type conversion is a function from a **source struct** to one or more
**target structs**. Before writing any logic, pin down both ends.

**Source — the AI Gateway model** (`internal/aigw/model.go`):

```go
type Model struct {
    Type         string        // "model" | "api"
    Name         string
    Capabilities []string      // [generate], [embeddings], [audio], ...
    Formats      []Format      // wire format: openai | anthropic | gemini | bedrock | ...
    TargetModels []TargetModel // one or more backend models (the load-balancer pool)
    Policies     []string      // policy refs → scoped plugins
    ACLs         ACLs          // → acl plugin
    Config       ModelConfig   // route paths, balancer, streaming, body-size, alias
    ...
}
```

A `TargetModel` references a `Provider` **by name** and carries the provider
`type` plus a free-form options map (`internal/aigw/targetmodel.go`):

```go
type TargetModel struct {
    Name              string            // upstream model name, e.g. "gpt-5.2"
    Weight            *int
    AllowAuthOverride *bool
    Provider          ProviderRef       // → Provider by name
    Config            TargetModelConfig // {type, options...}
}
```

**Target — the Kong decK structs** (`internal/kong/deck.go`): `Service`,
`Route`, `Plugin`, `AIModel`. A model lowers into:

- one shared `ai-gateway` **Service** (placeholder URL),
- one **Route** per distinct endpoint,
- an `ai-proxy-advanced` **Plugin** (and usually an `ai-model-selector`) per route,
- one `ai-models` **entry** per source model,
- top-level **policy/ACL plugins** scoped to the model via a `model:` FK.

> Providers are **not** standalone Kong entities. Their auth and options are
> folded into each `ai-proxy-advanced` target. That folding is the second half
> of this guide.

---

## 1. Resolve cross-references up front

References are resolved by name through registries built before any
conversion runs (`convert/registry.go`, called first in `run()`):

```go
c.providers[p.Name] = p   // TargetModel.Provider.Name → *aigw.Provider
c.policies[p.Name]  = p
c.consumerGroups[...] = ...
```

In `convertModels` the provider lookup is `provider := c.providers[tm.Provider.Name]`.
A missing reference is **not** fatal by default — it goes through `c.warn(...)`,
which appends a warning and continues, **or** returns an error in `-strict`
mode. Every `warn` call site must propagate the returned error:

```go
if provider == nil {
    if err := c.warn("model %q target %q references unknown provider %q; ...",
        m.Name, tm.Name, tm.Provider.Name); err != nil {
        return err
    }
}
```

This is the standard error/warning contract for every conversion step.

---

## 2. Pick the endpoint section

A model can serve multiple **capabilities** (`generate`, `embeddings`,
`audio`, …), and each maps to a different Kong route. The route shape comes
from the **endpoint table**, keyed by `(section, capability)`.

The **section** is chosen from the model's `llm_format` (the client-facing
wire format), *not* the provider type — `aimap.SectionFor` (`internal/aimap/endpoints.go`):

```go
sec := aimap.SectionFor(llmFormat(m), providerType)
// format == "" → "openai"
```

---

## 3. Expand and normalize capabilities

Raw capabilities are normalized to canonical keys before lookup
(`convert/model.go` → `expandCapabilities` → `aimap.NormalizeCapability`):

- loose spellings collapse: `chat` → `generate`, `batch` → `batches`,
- `audio` fans out to three: `audio/speech`, `audio/transcription`, `audio/translation`.

```go
caps := c.expandCapabilities(m)        // []string of canonical keys
for _, capability := range caps {
    spec, ok := aimap.LookupEndpoint(sec, capability)   // EndpointSpec
    ...
}
```

`EndpointSpec` is the heart of the model path. It carries everything needed to
build the route and target:

```go
type EndpointSpec struct {
    RouteLabel     string   // route-name suffix: "chat", "invoke", "embeddings"
    PathSuffix     string   // appended to base path; a regex body when IsRegex
    IsRegex        bool     // emit a Kong "~"-prefixed regex route
    Methods        []string
    RouteType      string   // ai-proxy-advanced target route_type, e.g. "llm/v1/chat"
    GenaiCategory  string   // ai-proxy-advanced config.genai_category
    TakesBodyModel bool     // request body carries a `model` field → add ai-model-selector
}
```

> When you add provider/capability support, you edit **`aimap.EndpointTable`**
> and **`aimap.NormalizeCapability`** — never the convert logic. Both directions
> read this one table, so forward and reverse cannot drift. The source of truth
> for these entries is `ref/supported-endpoints.md`.

---

## 4. Group everything that maps to the same route

Multiple `(model, target, capability)` tuples can resolve to the **same**
`(section, RouteLabel)` — they must collapse into **one route** carrying
multiple `ai-proxy-advanced` targets. That is what the `routeGroup`
accumulator does, keyed by `sec + "|" + spec.RouteLabel`:

```go
key := sec + "|" + spec.RouteLabel
g := groups[key]
if g == nil {
    g = &routeGroup{
        routeName:      sec + "-" + spec.RouteLabel,        // e.g. "openai-chat"
        path:           aimap.RoutePath(base, spec),        // base + suffix (regex-aware)
        methods:        spec.Methods,
        takesBodyModel: spec.TakesBodyModel,
        llmFormat:      llmFormat(m),
        genaiCategory:  spec.GenaiCategory,
        balancer:       balancerConfig(m.Config.Balancer),
        ...
        seen: map[string]bool{},
    }
    groups[key] = g
    order = append(order, key)        // preserve deterministic output order
}
```

Targets are de-duplicated within a group by `tm.Name + "|" + spec.RouteType`
so the same backend model isn't added twice:

```go
target := c.buildTarget(tm, provider, providerType, m.Config.Model.Alias, spec.RouteType)
dedup := tm.Name + "|" + spec.RouteType
if !g.seen[dedup] {
    g.seen[dedup] = true
    g.targets = append(g.targets, target)
}
```

`order` (a slice of keys) is maintained alongside the map so emitted YAML is
deterministic — important for golden tests.

---

## 5. Build one target (fold the provider in)

`buildTarget` (`convert/model.go`) constructs a single `ai-proxy-advanced`
target, delegating auth and option translation to `convert/provider.go`:

```go
model := map[string]any{
    "provider": aimap.PluginProvider(providerType),
    "name":     tm.Name,
}
if alias != "" { model["model_alias"] = alias }
if opts := mapOptions(tm.Config.Options, providerType, provider); opts != nil {
    model["options"] = opts
}
target := map[string]any{"route_type": routeType, "model": model}
if auth := resolveAuth(provider, tm.AllowAuthOverride); auth != nil {
    target["auth"] = auth
}
// weight, description added when present
```

**`resolveAuth`** (`convert/provider.go`) flattens a provider's auth union into
the target `auth` block — header/param auth, plus AWS / Azure / GCP variants
(`header_name`, `aws_access_key_id`, `azure_client_id`, `gcp_service_account_json`, …).

**`mapOptions`** translates the free-form options map, renaming/nesting
provider-specific keys:

- `azure`: `deployment_id` → `azure_deployment_id`, `api_version` → `azure_api_version`, provider `instance` → `azure_instance`
- `anthropic`: `version` → `anthropic_version`
- `gemini`: keys in `aimap.GeminiOptionKeys` nest under `options.gemini`; provider `project_id` too
- `bedrock`: keys in `aimap.BedrockOptionKeys` (and assume-role auth) nest under `options.bedrock`
- everything else passes through flat

> When adding a provider, the per-provider key handling lives in these two
> functions plus the `aimap.*OptionKeys` sets. Keep the reverse functions
> (`revert/provider.go` `defoldAuth`/`defoldOptions`) in lockstep.

---

## 6. Emit the ai-models entry and scoped guard plugins

Per source model, regardless of routes, emit one `ai-models` entry and turn the
model's policies/ACLs into top-level plugins scoped via a `model:` FK:

```go
c.out.AIModels = append(c.out.AIModels, kong.AIModel{
    Name:  m.Name,
    Alias: m.Config.Model.Alias,
})

plugins, _ := c.scopedPlugins(m.Policies, m.ACLs)   // convert/policy.go
for k := range plugins {
    plugins[k].Model = kong.NewRef(m.Name)          // scope to the ai-models entity
    guardPlugins = append(guardPlugins, plugins[k])
}
```

`scopedPlugins` skips global policies (emitted once at the top level by
`convertGlobalPolicies`) and warns on unknown policy refs.

---

## 7. Materialize the Service, Routes, and per-route plugins

After all models are walked, the accumulated groups become decK output. Note
the `ai-model-selector` plugin is emitted **only** when the endpoint
`TakesBodyModel`:

```go
service := kong.Service{Name: aimap.GatewayServiceName, URL: aimap.GatewayServiceURL}
for _, key := range order {
    g := groups[key]
    service.Routes = append(service.Routes, kong.Route{
        Name: g.routeName, Paths: []string{g.path}, Methods: g.methods,
        StripPath: boolPtr(false),
    })
    if g.takesBodyModel {
        c.out.Plugins = append(c.out.Plugins, kong.Plugin{
            Name:  "ai-model-selector",
            Route: kong.NewRef(g.routeName),
            Config: map[string]any{"source": "body", "body_path": "model",
                "max_request_body_size": g.bodySize},
        })
    }
    c.out.Plugins = append(c.out.Plugins, kong.Plugin{
        Name:   "ai-proxy-advanced",
        Route:  kong.NewRef(g.routeName),
        Config: g.proxyConfig(),     // balancer, llm_format, genai_category, targets, ...
    })
}
c.out.Services = append(c.out.Services, service)
c.out.Plugins = append(c.out.Plugins, guardPlugins...)
```

All model routes nest under the **one** shared `ai-gateway` Service with a
placeholder URL (`http://ai-gateway.upstream.local`); the real upstream is
selected per target by `ai-proxy-advanced` at runtime.

---

## End-to-end example

Source (`convert/testdata/01_single_model/input.yaml`):

```yaml
models:
  - name: gpt-5-2
    type: model
    capabilities: [generate]
    formats: [{type: openai}]
    config:
      route: {paths: [/ai]}
      model: {alias: "@openai/gpt-5.2"}
      response_streaming: allow
    target_models:
      - name: gpt-5.2
        provider: {name: openai-prod}
        config: {type: openai, temperature: 1.0, max_tokens: 1024}
providers:
  - name: openai-prod
    type: openai
    config:
      auth:
        type: basic
        headers: [{name: Authorization, value: "{vault://ai/openai-token}"}]
```

Result (`expected.yaml`) — note how each step above shows up:

```yaml
_format_version: "3.0"
services:
  - name: ai-gateway                       # §7 shared service
    url: http://ai-gateway.upstream.local
    routes:
      - name: openai-chat                  # §2/§4 section=openai, label=chat
        paths: [/ai/chat/completions]      # §3 base + spec.PathSuffix
        methods: [POST]
        strip_path: false
plugins:
  - name: ai-model-selector                # §7 because spec.TakesBodyModel
    config: {body_path: model, max_request_body_size: 8388608, source: body}
    route: {name: openai-chat}
  - name: ai-proxy-advanced
    config:
      balancer: {algorithm: round-robin}   # §4 balancerConfig default
      genai_category: text/generation      # §3 spec.GenaiCategory
      llm_format: openai
      response_streaming: allow
      targets:
        - auth: {header_name: Authorization, header_value: '{vault://ai/openai-token}'}  # §5 resolveAuth
          model:
            model_alias: '@openai/gpt-5.2'
            name: gpt-5.2
            options: {max_tokens: 1024, temperature: 1}   # §5 mapOptions
            provider: openai                                # §5 PluginProvider
          route_type: llm/v1/chat           # §3 spec.RouteType
    route: {name: openai-chat}
ai-models:                                  # §6
  - name: gpt-5-2
    alias: '@openai/gpt-5.2'
```

A multi-target model (`02_multi_target_balancer`) produces the **same single
route** with two entries in `targets[]` (§4); a model declaring several
capabilities produces several routes under the same service (§4 grouping).

---

## Checklist for a new type conversion

1. **Define both ends** — a source struct in `internal/aigw/` (with `omitempty`
   and any paired `Unmarshal`/`MarshalYAML`), and confirm the target decK
   structs in `internal/kong/deck.go` cover what you emit.
2. **Put shared mapping data in `internal/aimap/`** — never inline mapping
   tables in the convert/revert logic, so both directions stay in sync.
3. **Add a `convert<Type>()` method** in its own file, and call it in the
   fixed order in `Converter.run()`.
4. **Resolve references through the registries**; route every unresolved ref
   and unsupported feature through `c.warn(...)` and propagate its error.
5. **Keep output deterministic** (maintain an explicit `order` slice alongside
   any grouping map) so golden tests are stable.
6. **Mirror it in `revert/`** and keep the round trip byte-identical with zero
   warnings (`revert/roundtrip_test.go`).
7. **Add a golden case** under `convert/testdata/<case>/`, run
   `go test ./convert -run TestGolden -update`, and **review** the generated
   `expected.yaml` before committing.
