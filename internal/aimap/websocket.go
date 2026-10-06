package aimap

// webSocketPlugins holds the plugins whose schema accepts the ws and wss
// protocols. The list comes from the plugin schemas of Kong AI Gateway 2.0.2,
// 2.1, and 2.2.0, which carry the identical set. Re-verify it against a newer
// gateway release before trusting it there.
//
// A plugin missing from this list is treated as unsupported: a policy plugin
// is dropped from a realtime route with a warning (see convertModels), and an
// auth strategy plugin fails the conversion outright. Both read safer than
// the alternative of guessing wrong, since a data plane refuses to load the
// whole configuration when any one plugin declares a protocol its own schema
// does not accept.
var webSocketPlugins = map[string]bool{
	"acl": true, "ai-proxy": true, "ai-proxy-advanced": true, "ai-rate-limiting-advanced": true, "azure-functions": true,
	"basic-auth": true, "confluent-consume": true, "datadog": true, "file-log": true,
	"grpc-gateway": true, "grpc-web": true, "hmac-auth": true, "http-log": true,
	"ip-restriction": true, "kafka-consume": true, "kafka-log": true, "key-auth": true,
	"key-auth-enc": true, "ldap-auth": true, "ldap-auth-advanced": true, "loggly": true,
	"metering-and-billing": true, "mtls-auth": true, "oauth2": true, "openid-connect": true,
	"opentelemetry": true, "post-function": true, "pre-function": true, "prometheus": true,
	"proxy-cache": true, "request-transformer": true, "session": true, "solace-log": true,
	"statsd": true, "statsd-advanced": true, "syslog": true, "tcp-log": true, "udp-log": true,
	"websocket-size-limit": true, "websocket-validator": true, "zipkin": true,
}

// SupportsWebSocket reports whether the plugin named name runs on ws and wss
// routes.
func SupportsWebSocket(name string) bool {
	return webSocketPlugins[name]
}
