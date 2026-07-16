package aigw

import internal "github.com/Kong/ai-deck-converter/internal/aigw"

type (
	Document          = internal.Document
	Model             = internal.Model
	Format            = internal.Format
	ModelConfig       = internal.ModelConfig
	ModelSelector     = internal.ModelSelector
	Balancer          = internal.Balancer
	TargetModel       = internal.TargetModel
	TargetModelConfig = internal.TargetModelConfig
	Provider          = internal.Provider
	ProviderConfig    = internal.ProviderConfig
	ProviderAuth      = internal.ProviderAuth
	AuthHeader        = internal.AuthHeader
	AuthParam         = internal.AuthParam
	Policy            = internal.Policy
	RouteConfig       = internal.RouteConfig
	CIDRPort          = internal.CIDRPort
	Logging           = internal.Logging
	ACLs              = internal.ACLs
	Labels            = internal.Labels
	Consumer          = internal.Consumer
	ConsumerGroup     = internal.ConsumerGroup
	Credential        = internal.Credential
	MCPServer         = internal.MCPServer
	Agent             = internal.Agent
	Vault             = internal.Vault

	// Field types of the above, re-exported so external consumers can build them as
	// composite literals (previously only reachable via field assignment).
	ModelAccess      = internal.ModelAccess
	AccessConfig     = internal.AccessConfig
	IdentityProvider = internal.IdentityProvider
	AgentConfig      = internal.AgentConfig
	MCPServerConfig  = internal.MCPServerConfig
	MCPAccess        = internal.MCPAccess
	MCPConfigAccess  = internal.MCPConfigAccess
	MCPTool          = internal.MCPTool
	ProxyConfig      = internal.ProxyConfig
	ProxyAuth        = internal.ProxyAuth
	ProxyHost        = internal.ProxyHost
)

func Parse(data []byte) (*Document, error) {
	return internal.Parse(data)
}
