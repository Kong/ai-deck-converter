package aimap

// BedrockOverrideHeaderName and BedrockOverrideHeaderValue fill the target auth
// of a Bedrock target that has allow_override and no header. The data plane
// rejects such a target without an Authorization header_name and a
// header_value. The data plane uses header_value as a Bedrock key only if it
// starts with "Bearer ", so the placeholder value keeps IAM auth.
const (
	BedrockOverrideHeaderName  = "Authorization"
	BedrockOverrideHeaderValue = "client-provided"
)
