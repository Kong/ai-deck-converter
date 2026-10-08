package aimap

// PluginPriority holds the static Kong handler PRIORITY of every plugin
// bundled with kong-ee, keyed by plugin name. The converter has no runtime
// access to Kong's actual priorities, so this table is a hand-maintained copy
// of each plugin's `kong/plugins/<name>/handler.lua` (or
// `plugins-ee/<name>/.../handler.lua`) PRIORITY constant. Re-sync it when
// kong-ee changes a priority or ships a new plugin; a plugin missing from
// this table is simply not covered by OutranksModelSelector.
var PluginPriority = map[string]int{
	"pre-function":                   1000000,
	"app-dynamics":                   999999,
	"correlation-id":                 100001,
	"zipkin":                         100000,
	"exit-transformer":               9999,
	"bot-detection":                  2500,
	"cors":                           2000,
	"jwe-decrypt":                    1999,
	"session":                        1900,
	"acme":                           1705,
	"oauth2-introspection":           1700,
	"mtls-auth":                      1600,
	"degraphql":                      1500,
	"jwt":                            1450,
	"oauth2":                         1400,
	"vault-auth":                     1350,
	"key-auth-enc":                   1250,
	"key-auth":                       1250,
	"ldap-auth":                      1200,
	"ldap-auth-advanced":             1200,
	"basic-auth":                     1100,
	"openid-connect":                 1050,
	"hmac-auth":                      1030,
	"jwt-signer":                     1020,
	"ai-mcp-oauth2":                  1015,
	"saml":                           1010,
	"json-threat-protection":         1009,
	"header-cert-auth":               1009,
	"xml-threat-protection":          1008,
	"injection-protection":           1007,
	"websocket-validator":            1006,
	"websocket-size-limit":           1003,
	"request-validator":              999,
	"grpc-gateway":                   998,
	"tls-handshake-modifier":         997,
	"tls-metadata-headers":           996,
	"application-registration":       995,
	"ip-restriction":                 990,
	"konnect-application-auth":       960,
	"ai-model-selector":              957,
	"ace":                            955,
	"request-size-limiting":          951,
	"acl":                            950,
	"opa":                            920,
	"rate-limiting-advanced":         910,
	"rate-limiting":                  910,
	"ai-rate-limiting-advanced":      905,
	"graphql-rate-limiting-advanced": 902,
	"service-protection":             901,
	"response-ratelimiting":          900,
	"route-by-header":                850,
	"oas-validation":                 840,
	"ai-mcp-proxy":                   820,
	"ai-a2a-proxy":                   819,
	"request-callout":                812,
	"jq":                             811,
	"datakit":                        810,
	"request-transformer-advanced":   802,
	"request-transformer":            801,
	"response-transformer-advanced":  800,
	"response-transformer":           800,
	"ai-nvidia-nemo-guardrail":       786,
	"ai-custom-guardrail":            785,
	"ai-lakera-guard":                784,
	"ai-gcp-model-armor":             783,
	"ai-semantic-response-guard":     782,
	"ai-aws-guardrails":              781,
	"route-transformer-advanced":     780,
	"redirect":                       779,
	"ai-sanitizer":                   776,
	"ai-semantic-prompt-guard":       775,
	"ai-azure-content-safety":        774,
	"ai-prompt-template":             773,
	"ai-prompt-decorator":            772,
	"ai-prompt-guard":                771,
	"ai-proxy-advanced":              770,
	"ai-proxy":                       770,
	"ai-prompt-compressor":           769,
	"ai-response-transformer":        768,
	"ai-llm-as-judge":                767,
	"ai-semantic-cache":              765,
	"upstream-oauth":                 760,
	"standard-webhooks":              759,
	"solace-consume":                 756,
	"solace-upstream":                755,
	"confluent-consume":              754,
	"kafka-consume":                  753,
	"confluent":                      752,
	"kafka-upstream":                 751,
	"aws-lambda":                     750,
	"azure-functions":                749,
	"token-vault-exchange":           740,
	"ai-request-transformer":         777,
	"ai-rag-injector":                778,
	"proxy-cache-advanced":           100,
	"proxy-cache":                    100,
	"graphql-proxy-cache-advanced":   99,
	"forward-proxy":                  50,
	"upstream-timeout":               400,
	"canary":                         20,
	"metering-and-billing":           16,
	"solace-log":                     15,
	"opentelemetry":                  14,
	"prometheus":                     13,
	"http-log":                       12,
	"statsd":                         11,
	"statsd-advanced":                11,
	"datadog":                        10,
	"file-log":                       9,
	"udp-log":                        8,
	"tcp-log":                        7,
	"loggly":                         6,
	"kafka-log":                      5,
	"syslog":                         4,
	"grpc-web":                       3,
	"request-termination":            2,
	"mocking":                        -1,
	"post-function":                  -1000,
}

// AIModelSelectorPriority is PluginPriority["ai-model-selector"], the static
// priority a model-scoped policy must outrank to need reordering.
const AIModelSelectorPriority = 957

// OutranksModelSelector reports whether pluginType's static Kong priority
// runs before ai-model-selector by default. A model-scoped instance of such a
// plugin would execute before the model is selected unless reordered.
// Unknown plugin types (custom/community plugins not in PluginPriority)
// report false: their priority can't be known here.
//
// Callers attach an ordering block for a true pluginType, but this has no
// runtime effect yet on a model-scoped plugin instance: see "Assumptions and
// limitations" in README.md.
func OutranksModelSelector(pluginType string) bool {
	p, ok := PluginPriority[pluginType]
	return ok && p > AIModelSelectorPriority
}
