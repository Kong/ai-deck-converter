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
	)
}
