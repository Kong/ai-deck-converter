package convert

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
	"github.com/stretchr/testify/require"
)

func TestRoutesOverlap(t *testing.T) {
	port := func(p int) *int { return &p }
	base := kong.Route{Paths: []string{"/a"}}
	for name, tc := range map[string]struct {
		a, b kong.Route
		want bool
	}{
		"equal paths":             {base, base, true},
		"different paths":         {base, kong.Route{Paths: []string{"/b"}}, false},
		"prefix paths":            {base, kong.Route{Paths: []string{"/a/b"}}, false},
		"no paths":                {kong.Route{}, base, true},
		"one shared path":         {base, kong.Route{Paths: []string{"/b", "/a"}}, true},
		"regex and plain path":    {kong.Route{Paths: []string{"~/a"}}, base, false},
		"equal regex paths":       {kong.Route{Paths: []string{"~/a$"}}, kong.Route{Paths: []string{"~/a$"}}, true},
		"methods without case":    {kong.Route{Methods: []string{"get"}}, kong.Route{Methods: []string{"GET"}}, true},
		"different methods":       {kong.Route{Methods: []string{"GET"}}, kong.Route{Methods: []string{"PUT"}}, false},
		"methods and no methods":  {kong.Route{Methods: []string{"GET"}}, kong.Route{}, true},
		"default protocols":       {kong.Route{Protocols: []string{"https"}}, kong.Route{}, true},
		"protocol not in default": {kong.Route{Protocols: []string{"ws", "wss"}}, kong.Route{}, false},
		"protocols without case":  {kong.Route{Protocols: []string{"HTTP"}}, kong.Route{Protocols: []string{"http"}}, true},
		"different hosts":         {kong.Route{Hosts: []string{"a.test"}}, kong.Route{Hosts: []string{"b.test"}}, false},
		"hosts and no hosts":      {kong.Route{Hosts: []string{"a.example.com"}}, kong.Route{}, true},
		"different snis":          {kong.Route{SNIs: []string{"a.example"}}, kong.Route{SNIs: []string{"b.example"}}, false},
		"header on one route": {
			kong.Route{Headers: map[string][]string{"x-a": {"1"}}},
			kong.Route{Headers: map[string][]string{"x-b": {"2"}}},
			true,
		},
		"header values without case": {
			kong.Route{Headers: map[string][]string{"X-Tenant": {"Blue"}}},
			kong.Route{Headers: map[string][]string{"x-tenant": {"blue", "red"}}},
			true,
		},
		"different header values": {
			kong.Route{Headers: map[string][]string{"x-tenant": {"a"}}},
			kong.Route{Headers: map[string][]string{"X-Tenant": {"b"}}},
			false,
		},
		"regex and plain header value": {
			kong.Route{Headers: map[string][]string{"x-tenant": {"~*a"}}},
			kong.Route{Headers: map[string][]string{"x-tenant": {"a"}}},
			false,
		},
		"equal regex header values": {
			kong.Route{Headers: map[string][]string{"x-tenant": {"~*^a$"}}},
			kong.Route{Headers: map[string][]string{"x-tenant": {"~*^a$"}}},
			true,
		},
		"overlapping sources": {
			kong.Route{Sources: []kong.CIDRPort{{IP: "10.0.0.0/8"}}},
			kong.Route{Sources: []kong.CIDRPort{{IP: "10.1.2.3"}}},
			true,
		},
		"separate sources": {
			kong.Route{Sources: []kong.CIDRPort{{IP: "10.0.0.0/8"}}},
			kong.Route{Sources: []kong.CIDRPort{{IP: "192.168.0.0/16"}}},
			false,
		},
		"different source ports": {
			kong.Route{Sources: []kong.CIDRPort{{IP: "10.0.0.0/8", Port: port(80)}}},
			kong.Route{Sources: []kong.CIDRPort{{IP: "10.0.0.0/8", Port: port(443)}}},
			false,
		},
		"source port and no port": {
			kong.Route{Sources: []kong.CIDRPort{{IP: "10.0.0.0/8", Port: port(80)}}},
			kong.Route{Sources: []kong.CIDRPort{{IP: "10.0.0.1"}}},
			true,
		},
		"port-only destination": {
			kong.Route{Destinations: []kong.CIDRPort{{Port: port(80)}}},
			kong.Route{Destinations: []kong.CIDRPort{{IP: "10.0.0.1", Port: port(80)}}},
			true,
		},
		"different destinations": {
			kong.Route{Destinations: []kong.CIDRPort{{IP: "10.0.0.1"}}},
			kong.Route{Destinations: []kong.CIDRPort{{IP: "10.0.0.2"}}},
			false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, routesOverlap(tc.a, tc.b))
			require.Equal(t, tc.want, routesOverlap(tc.b, tc.a))
		})
	}
}

func TestHostsOverlap(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"example.com", "EXAMPLE.com", true},
		{"example.com", "api.example.com", false},
		{"*.example.com", "api.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "*.api.example.com", true},
		{"*.xexample.com", "*.example.com", false},
		{"api.*", "api.example.com", true},
		{"api.*", "apiexample.com", false},
		{"a.*", "a.b.*", true},
		{"a.*", "ab.*", false},
		{"*.com", "example.*", true},
		{"example.com:8080", "example.com:8080", true},
		{"example.com:8080", "example.com", false},
	} {
		t.Run(tc.a+" "+tc.b, func(t *testing.T) {
			require.Equal(t, tc.want, hostsOverlap(tc.a, tc.b))
			require.Equal(t, tc.want, hostsOverlap(tc.b, tc.a))
		})
	}
}

func TestRoutingConflictReportsLaterEntity(t *testing.T) {
	result, err := Convert([]byte(`
mcp_servers:
  - type: passthrough-listener
    name: first
    upstream_url: https://first.example.com/mcp
    config: {route: {paths: [/mcp/flights]}}
  - type: passthrough-listener
    name: second
    upstream_url: https://second.example.com/mcp
    config: {route: {paths: [/mcp/flights]}}
`), testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
	issue := requireIssue(t, result.Issues, Warning, RoutingConflict, kindMCPServer, "second", "config.route")
	require.Contains(t, issue.Reason, `mcp_server "first"`)
}

func TestRoutingConflictReportsUnsetPaths(t *testing.T) {
	result, err := Convert([]byte(`
mcp_servers:
  - {type: passthrough-listener, name: first, upstream_url: https://first.example.com/mcp}
  - {type: passthrough-listener, name: second, upstream_url: https://second.example.com/mcp}
`), testOptions)
	require.NoError(t, err)
	requireIssue(t, result.Issues, Warning, RoutingConflict, kindMCPServer, "second", "config.route")
}

func TestRoutingConflictSkipsDisabledServices(t *testing.T) {
	result, err := Convert([]byte(`
mcp_servers:
  - type: passthrough-listener
    name: off
    enabled: false
    upstream_url: https://off.example.com/mcp
    config: {route: {paths: [/shared]}}
agents:
  - type: http
    name: on
    config: {url: https://on.internal, route: {paths: [/shared]}}
  - type: http
    name: also-off
    enabled: false
    config: {url: https://off.internal, route: {paths: [/shared]}}
`), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
}

func TestRoutingConflictLeavesOutModelWithUsedAlias(t *testing.T) {
	result, err := Convert([]byte(`
model_providers: [{name: openai, type: openai}]
models:
  - name: a
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/a]}}
  - name: b
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/b], model: {values: [a]}}}
  - name: a
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/c]}}
`), testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 2)
	requireIssue(t, result.Issues, Error, RoutingConflict, kindModel, "b", "config.route.model.values")
	var fields [][]string
	for _, issue := range result.Issues {
		fields = append(fields, issue.Source.Fields)
	}
	require.Contains(t, fields, []string{"name"})
	require.Len(t, result.Output.AIModels, 1)
	require.Equal(t, "a", result.Output.AIModels[0].Name)
}

// A disabled model makes no output, so it cannot conflict.
func TestRoutingConflictIgnoresDisabledModel(t *testing.T) {
	result, err := Convert([]byte(`
model_providers: [{name: openai, type: openai}]
models:
  - name: off
    enabled: false
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/a], model: {values: [gpt]}}}
  - name: on
    capabilities: [generate]
    access: {auth_strategies: [keys]}
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/a], model: {values: [gpt]}}}
auth_strategies: [{name: keys, type: key-auth, config: {}}]
`), testOptions)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
}

func TestRoutingConflictReportsSelectorPairOnce(t *testing.T) {
	result, err := Convert([]byte(`
model_providers: [{name: openai, type: openai}]
models:
  - name: body
    capabilities: [generate, embeddings]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/s]}}
  - name: header
    capabilities: [generate, embeddings]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/s], model: {header: {header_param: x-model}}}}
  - name: other-body
    capabilities: [generate]
    targets: [{name: gpt-5, provider: openai}]
    config: {route: {paths: [/s], model: {values: [other]}}}
`), testOptions)
	require.NoError(t, err)
	require.Len(t, result.Issues, 2, "issues: %v", result.Issues)
	requireIssue(t, result.Issues, Warning, RoutingConflict, kindModel, "header", "config.route.model")
	issue := requireIssue(t, result.Issues, Warning, RoutingConflict, kindModel, "other-body", "config.route.model")
	require.Contains(t, issue.Reason, `model "header"`)
}

func TestSharedLifecycleRoutes(t *testing.T) {
	lifecycle := kong.Route{Name: "a", Paths: []string{"/v/videos"}, Tags: []string{aimap.VideoLifecycleRouteTag}}
	other := lifecycle
	other.Name = "b"
	require.True(t, sharedLifecycleRoutes(lifecycle, other))
	other.Hosts = []string{"api.example.com"}
	require.False(t, sharedLifecycleRoutes(lifecycle, other))
	require.False(t, sharedLifecycleRoutes(lifecycle, kong.Route{Paths: lifecycle.Paths}))
}
