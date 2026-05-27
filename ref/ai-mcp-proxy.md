---
title: AI MCP Proxy Plugin
description: 'Convert APIs into MCP tools, proxy MCP servers, expose multiple MCP
  tools for AI clients, and observe MCP traffic in real time.

  '
url: "/plugins/ai-mcp-proxy/"
content_type: plugin
min_version:
  gateway: '3.12'
tier: ai_gateway_enterprise
products:
- Kong Gateway
- AI Gateway
tools:
- deck
- Admin API
- Konnect API
- KIC
- Operator
- Terraform
tags:
- ai
- mcp
canonical: true
works_on:
- on-prem
- konnect
topologies:
  on_prem:
  - hybrid
  - db-less
  - traditional
  konnect_deployments:
  - hybrid
  - cloud-gateways
  - serverless
publisher: kong-inc
compatible_protocols:
- grpc
- grpcs
- http
- https
categories:
- ai


---

# AI MCP Proxy Plugin






The AI MCP Proxy plugin lets you connect any Kong-managed Service to the [Model Context Protocol (MCP)](https://modelcontextprotocol.io/). It acts as a **protocol bridge**, translating between MCP and HTTP so that MCP-compatible clients can either call existing APIs or interact with upstream MCP servers through Kong.

The plugin’s `mode` parameter controls whether it proxies MCP requests, converts RESTful APIs into MCP tools, or exposes grouped tools as an MCP server. This flexibility allows you to integrate existing HTTP APIs into MCP workflows, front third-party MCP servers with Kong’s policies, or expose multiple tool sets as a managed MCP server.

Because the plugin runs directly on AI Gateway, MCP endpoints are provisioned dynamically on demand. You don’t need to host or scale them separately, and the AI Gateway applies its authentication, traffic control, and observability features to MCP traffic at the same scale it delivers for traditional APIs.


> **Note:** Unlike other available AI plugins, the AI MCP Proxy plugin is not invoked as part of an LLM request flow.
> Instead, it's part of an MCP request flow. It's registered and executed as a regular plugin, between the MCP client and the MCP server, allowing it to capture MCP traffic independently of LLM request flow.
>
> **Do not configure the AI MCP Proxy plugin together with other AI plugins on the same Service or Route**.

## Why use the AI MCP Proxy plugin

The AI MCP Proxy bridges the Kong plugin ecosystem with the MCP world, enabling you to bring all of Kong's traffic management, security, and observability capabilities to MCP endpoints:


### Authentication
Example: Apply [OpenID Connect](/plugins/openid-connect/) or the [Key Auth](/plugins/key-auth/) plugin to an MCP Server

### Rate limiting
Example: Use [Rate Limiting](/plugins/rate-limiting/) or [Rate Limiting Advanced](/plugins/rate-limiting-advanced) plugins to control MCP request volume

### Observability
Example: Add [logging and tracing plugins](/plugins/?category=logging) for full request and response visibility

### Traffic control
Example: Apply [request/response transformation plugins](/plugins/?category=transformations) or [ACL policies](/plugins/acl/)




## How it works

The AI MCP Proxy plugin handles MCP requests by converting them into standard HTTP calls and returning the responses in MCP format. The flow works as follows:

1. Accepts MCP protocol requests from a client.
2. Parses the MCP tool call and matches it to an OpenAPI operation.
3. Converts the operation into a standard HTTP request.
4. Sends the request to the upstream Service.
5. Wraps the HTTP response in MCP-compatible format and returns it.


<pre class='mermaid'> 
sequenceDiagram
    participant C as MCP Client
    participant K as Kong (AI MCP Proxy plugin)
    participant U as Upstream Service

    C->>K: MCP request (tool invocation)
    activate K
    K->>K: Parse MCP payload
    K->>K: Map to HTTP endpoint (OpenAPI schema)
    K->>U: HTTP request
    deactivate K
    activate U
    U-->>K: HTTP response
    deactivate U
    activate K
    K->>K: Convert to MCP format
    K-->>C: MCP response
    deactivate K
  </pre>



> Pings from your MCP client are included in the total request count for your AI Gateway instance, in addition to the requests made to the MCP server.

## Prerequisites


> Before using the AI MCP Proxy plugin, ensure your setup meets these requirements:
> - **In `conversion-only` and `conversion-listener` modes, the plugin must be scoped to a Route.** Tool conversion requires Route information. If you apply the plugin to a Service without a Route, the plugin skips conversion and logs a warning. `passthrough-listener` mode does not require Route scoping.
> - **Consumer and Consumer Group scoping is not supported** in any mode.
> - The upstream Service exposes a valid OpenAPI schema.
> - That Service is configured and accessible in Kong.
> - An MCP-compatible client (such as [Insomnia](https://konghq.com/products/kong-insomnia), [Claude](https://claude.ai/), [Cursor](https://cursor.com/), or [LMstudio](https://lmstudio.ai/)) is available to connect to Kong.
> - The AI Gateway instance supports the AI MCP Proxy plugin (is 3.12 or higher).

## Configuration modes

The AI MCP Proxy plugin operates in four modes, controlled by the [`config.mode`](./reference/#schema--config-mode) parameter. Each mode determines how Kong handles MCP requests and whether it converts RESTful APIs into MCP tools.


### [`passthrough-listener`](./examples/passthrough-listener/)

Description: |
  Listens for incoming MCP requests and proxies them to the `upstream_url` of the Gateway Service.
  Generates MCP observability metrics for traffic, making it suitable for third-party MCP servers hosted by users.
Use cases: |
  Use when you already operate an MCP server and want Kong Gateway to act as an authenticated, observable entrypoint for it.
  Useful for exposing a third-party or internally hosted MCP service through Kong Gateway.

### [`conversion-listener`](./examples/conversion-listener/)

Description: |
  Converts RESTful API paths into MCP tools **and** accepts incoming MCP requests on the Route path.
  You can define tools directly in the plugin configuration and optionally set a server block.<br/><br/>
  v3.13+ The conversion-listener mode also supports adding session identifiers set by authentication services in the configuration parameters. See the [cookie conversion example](./examples/conversion-listener/) for details on handling cookie-based authentication.
Use cases: |
  Use when you want to make an existing REST API available to MCP clients directly through Kong Gateway.
  Common for services that both define and handle their own tools.

### [`conversion-only`](./examples/conversion-only/)

Description: |
  Converts RESTful API paths into MCP tools but does **not** accept incoming MCP requests.
  `tags` can be defined at the plugin level and are used by `listener` plugins to expose the tools. This mode does not define a server.<br/><br/>
  
  This mode must be used together with other AI MCP Proxy plugins configured with the `listener` mode.
Use cases: |
  Use when you want to define reusable tool specifications without serving them.
  Suitable for teams that maintain a shared library of tool definitions for other listener plugins.

### [`listener`](./examples/listener/)

Description: |
  Similar to `conversion-listener`, but instead of defining its own tools, it binds multiple `conversion-only` tools using the [`config.server.tag`](./reference/#schema--config-server-tag) property.
  `conversion-only` plugins define `tags` at the plugin level, and the listener connects to them to expose the tools on a Route for incoming MCP requests.<br/><br/>
  
  This mode must be used together with other AI MCP Proxy plugins configured with the `conversion-only` mode.
Use cases: |
  Use when you need a single MCP endpoint that aggregates tools from multiple `conversion-only` plugins.
  Typical in multi-service or multi-team environments that expose a unified MCP interface.




## ACL tool control v3.13+

When exposing MCP servers through Kong Gateway, you may need granular control over which authenticated API consumers can discover and invoke specific tools. The AI MCP Proxy plugin's ACL feature lets you define access rules at both the default level (applying to all tools) and per-tool level (for fine-grained exceptions)

This way, consumers only interact with tools appropriate to their role, while maintaining a complete audit trail of all access attempts. Authentication is handled by standard Kong AuthN plugins (for example, [Key Auth](/plugins/key-auth/) or OIDC flows), and the resulting Consumer identity is used for ACL checks.


> **ACL in `listener` mode**
>
> Listener mode does not support direct ACL configuration. Instead, it inherits ACL rules from tagged conversion-listener or conversion-only plugins.
>
> To use ACLs with `listener` mode:
> 1. Configure conversion-listener or conversion-only plugins with ACL rules and tags.
> 2. Configure listener mode to aggregate tools by matching tags.
> 3. Set `include_consumer_groups: true` on the listener. Without this setting, the listener cannot pass Consumer Group membership to the aggregated tools, and ACL rules will not evaluate correctly.
>
> See [Enforce ACLs on aggregated MCP servers](/mcp/enforce-acls-on-aggregated-mcp-servers/) for a complete example.

### Supported identifier types

ACL rules can reference [Consumers](/gateway/entities/consumer/) and [Consumer Groups](/gateway/entities/consumer-group/) using these identifier types in `allow` and `deny` lists:

* [`username`](/gateway/entities/consumer/#schema-consumer-username): Consumer username
* [`id`](/gateway/entities/consumer/#schema-consumer-username): Consumer UUID
* [`custom_id`](/gateway/entities/consumer/#schema-consumer-custom-id): Custom Consumer identifier
* [`consumer_groups.name`](/gateway/entities/consumer/#schema-consumer-custom-id): Consumer Group name

The authenticated Consumer identity is matched against these identifiers. If the [Consumer](/gateway/entities/consumer/) or any of their [Consumer Groups](/gateway/entities/consumer-group/) match an ACL entry, the rule applies.

### How default and per-tool ACLs work

The plugin evaluates access using a two-tier system:


#### [`default_acl`](./reference/#schema--config-default-acl)

Description: Baseline rules that apply to all tools unless overridden.

#### [`tools[].acl`](./reference/#schema--config-tools-acl)

Description: When configured, these rules replace the default ACL for that specific tool. The per-tool ACL doesn't inherit or merge with `default_acl`—it is an all-or-nothing override.





> If a tool defines its own ACL, the plugin ignores `default_acl` for that tool:
>
> - Tools with no ACL configuration inherit the default rules (both `allow` and `deny` lists)
> - Tools with an ACL must explicitly list all allowed subjects (even if they were already in `default_acl`)

### ACL evaluation logic

Both default and per-tool ACLs use `allow` and `deny` lists. Evaluation follows this order:

1. **Deny list configuration**: If a `deny` list exists and the subject matches any `deny` entry, the request is rejected (`HTTP 403 Forbidden`).
2. **Allow list configuration**: If an `allow` list exists, the subject must match at least one entry; otherwise, the request is denied (`HTTP 403 Forbidden`).
3. **No allow list configuration**: If no `allow` list exists and the subject is not in `deny`, the request is allowed.
4. **No ACL configuration**: If neither list exists, the request is allowed.

All access attempts (allowed or denied) are written to the plugin's audit log.

The table below summarizes the possible ACL configurations and their outcomes.

#### Subject matches any `deny` rule
Proxied to upstream service?: false
Response code: HTTP 403 Forbidden

#### `allow` list exists and subject is not in it
Proxied to upstream service?: false
Response code: HTTP 403 Forbidden

#### Only `deny` list exists and subject is not in it
Proxied to upstream service?: true
Response code: 200

#### No ACL rules configured
Proxied to upstream service?: true
Response code: 200



### ACL tool control request flow

The AI MCP Proxy plugin evaluates ACLs for both tool discovery and tool invocation. These are two distinct operations with different behaviors:

**Tool Discovery (List tools)**:
1. MCP client requests the list of available tools
2. Kong AuthN plugin validates the request and identifies the Consumer
3. AI MCP Proxy loads the Consumer's group memberships
4. Plugin evaluates each tool against the `default_acl`
5. Plugin returns an HTTP 200 response with only the tools the Consumer is allowed to access
6. Plugin logs the discovery attempt

**Tool invocation**:
1. MCP client invokes a specific tool
2. Kong AuthN plugin validates the request and identifies the Consumer
3. AI MCP Proxy loads the Consumer's group memberships
4. Plugin evaluates the tool-specific ACL if it exists, or the default ACL otherwise
5. Plugin logs the access attempt (allowed or denied)
6. Plugin returns `HTTP 403 Forbidden` if denied, or forwards the request to the upstream MCP server if allowed


<pre class='mermaid'> 
sequenceDiagram
  participant Client as MCP Client
  participant Kong as Kong Gateway
  participant Auth as AuthN Plugin
  participant ACL as ai-mcp-proxy (ACL/Audit)
  participant Up as Upstream MCP Server
  participant Log as Audit Sink

  %% ----- List Tools -----
  rect
    note over Client,Kong: List Tools (Default ACL Scope)
    Client->>Kong: GET /tools
    Kong->>Auth: Authenticate
    Auth-->>Kong: Consumer identity
    Kong->>ACL: Evaluate scoped default ACL
    ACL-->>Log: Audit entry
    alt If allowed
      Kong-->>Client: Filtered tool list
    else If denied
      Kong-->>Client: HTTP 403 Forbidden
    end
  end

  %% ----- Tool Invocation -----
  rect
    note over Client,Up: Tool Invocation (Per-tool ACL)
    Client->>Kong: POST /tools/{tool}
    Kong->>Auth: Authenticate
    Auth-->>Kong: Consumer identity
    Kong->>ACL: Evaluate per-tool ACL
    ACL-->>Log: Audit entry
    alt If allowed
      Kong->>Up: Forward request
      Up-->>Kong: Response
      Kong-->>Client: Response
    else If denied
      Kong-->>Client: HTTP 403 Forbidden
    end
  end
  </pre>


## Migration path

For users already using the AI MCP Proxy plugin without ACL configuration, follow these steps to add ACL tool control:
1. **Add an AuthN plugin**: Enable an authentication plugin such as [Key Auth](/plugins/key-auth/) to work with Consumers and Consumer Groups.
2. **Add ACL fields to the plugin configuration**: Update the AI MCP Proxy plugin schema to include `default_acl` and per-tool `acl` fields.
3. **Configure ACL rules**: Add `allow` and `deny` lists to control access at the default and per-tool levels.
4. **Enable audit logging**: Set `logging.log_audits: true` to monitor access attempts and verify ACL enforcement.

## Scope of support

The AI MCP Proxy plugin provides support for MCP operations and upstream interactions, while certain advanced features and non-HTTP protocols are not currently supported. The table below summarizes what is supported and what is outside the current scope.



### Features: Protocol
Description: Handling latest streamable HTTP with HTTP and HTTPS upstreams
Supported: Supported

### Features: OpenAPI operations
Description: Mapping MCP calls to upstream HTTP operations based on the OpenAPI schema
Supported: Supported

### Features: JSON format
Description: Handling standard JSON request and response bodies
Supported: Supported

### Features: Form-encoded data
Description: Handling `application/x-www-form-urlencoded`
Supported: Supported

### Features: SNI routing
Description: Converting SNI-only routes
Supported: Not Supported

### Features: Form and XML data
Description: Handling formats such as multipart/form-data or XML
Supported: Not Supported

### Features: Advanced MCP features
Description: Handling structured output, active notifications on tool changes, and session sharing between instances
Supported: Not Supported

### Features: Non-HTTP protocols
Description: Handling WebSocket and gRPC upstreams
Supported: Not Supported

### Features: AI Guardrails
Description: Applying guardrails to MCP AI plugin requests and responses
Supported: Not Supported





## FAQs

- Which MCP protocol version does the AI MCP Proxy plugin use?
  The AI MCP Proxy plugin uses MCP protocol version 2025-06-18.

- What MCP protocol versions are supported for upstream MCP servers?
  The AI MCP Proxy plugin supports these upstream MCP server protocol versions:
  * 2025-06-18
  * 2025-11-25
  
  Versions from 2024 are not supported.

- Can I apply the AI MCP Proxy plugin to a Gateway Service instead of a Route?
  The plugin can be applied to a Service or a Route, except in `conversion-only` and `conversion-listener` modes where the plugin requires Route information for tool conversion. Tool indexing skips plugins that are not attached to a Route. If no Route is present, the plugin skips conversion and logs a warning. Always scope the plugin to a Route when using these modes.
  
  `passthrough-listener` mode does not require Route scoping because it proxies MCP traffic directly to an upstream server without performing tool conversion.
  
  Consumer and Consumer Group scoping is not supported in any mode.

- Why do I see the error code `INVALID_PARAMS -32602` on failed requests?
  Prior to AI Gateway 3.14, requests that matched an MCP ACL deny rule or failed to match an allow list returned the JSON-RPC error code `INVALID_PARAMS -32602`.
  This has now changed to match the [MCP 2025-11-25 authorization specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#error-handling) and returns `HTTP 403 Forbidden`.


## Related Resources

- [About AI Gateway](/ai-gateway/)

- [Autogenerate serverless MCP](/mcp/map-api-to-mcp-tools/)

- [All AI Gateway plugins](/plugins/?category=ai)

- [Kong MCP traffic gateway](/mcp/)

- [Autogenerate MCP tools from a RESTful API](/mcp/map-api-to-mcp-tools/)

- [Control MCP tool access with Consumer and Consumer Group ACLs](/mcp/use-access-controls-for-mcp-tools/)

- [Enforce ACLs on aggregated MCP servers](/mcp/enforce-acls-on-aggregated-mcp-servers/)

- [MCP Registry in Konnect (tech preview)](/catalog/mcp-registry/)


## Next Steps

- [Learn about Kong MCP traffic gateway](/mcp/)

- [Learn about Kong Konnect MCP Server](/mcp/kong-mcp/get-started/)

- [Autogenerate MCP tools from a RESTful API](/mcp/map-api-to-mcp-tools/)

- [Autogenerate MCP tools for Weather API](/mcp/map-weather-api-to-mcp-tools/)

- [Control MCP tool access with Consumer and Consumer Group ACLs](/mcp/use-access-controls-for-mcp-tools/)

- [Enforce ACLs on aggregated MCP servers](/mcp/enforce-acls-on-aggregated-mcp-servers/)

