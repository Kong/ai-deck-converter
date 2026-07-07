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
)

func Parse(data []byte) (*Document, error) {
	return internal.Parse(data)
}
