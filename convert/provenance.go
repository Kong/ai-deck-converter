package convert

import "github.com/Kong/ai-deck-converter/internal/kong"

func source(entityType, entityName, fieldPrefix string, mappings ...kong.FieldMapping) *kong.Source {
	return &kong.Source{
		EntityType:    entityType,
		EntityName:    entityName,
		FieldPrefix:   fieldPrefix,
		FieldMappings: mappings,
	}
}

func sourceScopedPlugins(plugins []kong.Plugin, entityType, entityName string) []kong.Plugin {
	for i := range plugins {
		if plugins[i].Name == "acl" {
			plugins[i].Source = source(entityType, entityName, "access.acls")
		}
	}
	return plugins
}

func serviceURLSource(entityType, entityName string) *kong.Source {
	return source(entityType, entityName, "config.url",
		kong.FieldMapping{GeneratedPrefix: "url", SourcePrefix: "config.url"},
		kong.FieldMapping{GeneratedPrefix: "host", SourcePrefix: "config.url"},
		kong.FieldMapping{GeneratedPrefix: "port", SourcePrefix: "config.url"},
		kong.FieldMapping{GeneratedPrefix: "protocol", SourcePrefix: "config.url"},
		kong.FieldMapping{GeneratedPrefix: "path", SourcePrefix: "config.url"},
		kong.FieldMapping{GeneratedPrefix: "client_certificate", SourcePrefix: "config.certificates"},
		kong.FieldMapping{GeneratedPrefix: "ca_certificates", SourcePrefix: "config.ca_certificates"},
	)
}

func agentTargetsServiceSource(entityName string) *kong.Source {
	return source("agent", entityName, "config.targets",
		kong.FieldMapping{GeneratedPrefix: "host", SourcePrefix: "config.targets"},
		kong.FieldMapping{GeneratedPrefix: "protocol", SourcePrefix: "config.protocol"},
		kong.FieldMapping{GeneratedPrefix: "path", SourcePrefix: "config.path"},
		kong.FieldMapping{GeneratedPrefix: "client_certificate", SourcePrefix: "config.certificates"},
		kong.FieldMapping{GeneratedPrefix: "ca_certificates", SourcePrefix: "config.ca_certificates"},
	)
}

func agentUpstreamSource(entityName string) *kong.Source {
	return source("agent", entityName, "config.balancer",
		kong.FieldMapping{GeneratedPrefix: "name", SourcePrefix: "name"},
		kong.FieldMapping{GeneratedPrefix: "healthchecks", SourcePrefix: "config.targets"},
		kong.FieldMapping{GeneratedPrefix: "hash_on", SourcePrefix: "config.balancer.hash_on_header"},
	)
}
