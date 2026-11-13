package aimap

import "slices"

// IsWebSocketProtocol reports whether protocol is ws or wss.
func IsWebSocketProtocol(protocol string) bool {
	return protocol == "ws" || protocol == "wss"
}

// IsHTTPProtocol reports whether protocol is http or https.
func IsHTTPProtocol(protocol string) bool {
	return protocol == "http" || protocol == "https"
}

// ProtocolsServe reports whether the model route protocols select spec under
// "*". An empty list is the Kong default of http and https.
func ProtocolsServe(protocols []string, spec EndpointSpec) bool {
	if IsWebSocketEndpoint(spec) {
		return slices.ContainsFunc(protocols, IsWebSocketProtocol)
	}
	return len(protocols) == 0 || slices.ContainsFunc(protocols, IsHTTPProtocol)
}

// TransportProtocols returns protocols without the protocols of the other
// transport. A WebSocket route drops http and https. An HTTP route drops ws
// and wss.
func TransportProtocols(protocols []string, websocket bool) []string {
	other := IsWebSocketProtocol
	if websocket {
		other = IsHTTPProtocol
	}
	return slices.DeleteFunc(slices.Clone(protocols), other)
}

// RouteProtocols returns the protocols of a WebSocket or HTTP route that one
// model route config makes. A list with no protocol of the transport changes
// each protocol to its counterpart. The counterpart keeps the TLS choice.
func RouteProtocols(protocols []string, websocket bool) []string {
	if websocket {
		if own := slices.DeleteFunc(slices.Clone(protocols), func(p string) bool {
			return !IsWebSocketProtocol(p)
		}); len(own) > 0 {
			return own
		}
		out := counterparts(protocols, map[string]string{"http": "ws", "https": "wss"}, false)
		if len(out) == 0 {
			return []string{"ws", "wss"}
		}
		return out
	}
	if slices.ContainsFunc(protocols, IsHTTPProtocol) || !slices.ContainsFunc(protocols, IsWebSocketProtocol) {
		return TransportProtocols(protocols, false)
	}
	return counterparts(protocols, map[string]string{"ws": "http", "wss": "https"}, true)
}

// counterparts changes each protocol through counterpart and removes
// duplicates. keepOthers keeps a protocol that counterpart does not hold.
func counterparts(protocols []string, counterpart map[string]string, keepOthers bool) []string {
	var out []string
	for _, p := range protocols {
		mapped, ok := counterpart[p]
		switch {
		case ok:
			p = mapped
		case !keepOthers:
			continue
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}
