package aimap

import "github.com/Kong/ai-deck-converter/internal/aigw"

// KongStargateProvider is the Stargate provider type.
const KongStargateProvider = aigw.KongStargateProvider

// KongStargateLaunchRegion is the only supported Stargate region.
const KongStargateLaunchRegion = "us"

// KongStargateEndpointURL gives the default URL for a Stargate operation.
func KongStargateEndpointURL(format string, spec EndpointSpec, region string) (string, bool) {
	if region != KongStargateLaunchRegion {
		return "", false
	}
	var path string
	switch {
	case format == "openai" && spec.RouteLabel == "chat":
		path = "/v1/chat/completions"
	case format == "openai" && spec.RouteLabel == "responses":
		path = "/v1/responses"
	case format == "anthropic" && spec.RouteLabel == "messages":
		path = "/v1/messages"
	default:
		return "", false
	}
	return "https://" + region + ".inference.konghq.com" + path, true
}
