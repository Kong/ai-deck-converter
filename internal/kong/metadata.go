package kong

import (
	"slices"

	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// ConversionMetadata identifies the AI Gateway source for generated plugin
// targets. It is sidecar metadata and is never included in the emitted Kong
// configuration.
type ConversionMetadata struct {
	PluginTargets []PluginTargetSource
	Plugins       []GeneratedEntitySource
	Routes        []GeneratedEntitySource
	Services      []GeneratedEntitySource
}

// PluginTargetSource identifies one target in the converted plugin list and
// the source model target and capability (or capabilities, when distinct
// capabilities collapse onto the same target) that produced it.
type PluginTargetSource struct {
	PluginIndex      int
	Location         string
	TargetIndex      int
	ModelName        string
	ModelTargetIndex int
	Capabilities     []string
	CapabilityLabels []string
}

// GeneratedEntitySource identifies the source API entity for a generated
// plugin, route, or service. FieldPrefix is used for direct field mappings;
// FieldMappings handle generated field names that differ from the API model.
type GeneratedEntitySource struct {
	Index         int
	Location      string
	EntityType    string
	EntityName    string
	FieldPrefix   string
	FieldMappings []FieldMapping
}

func (m *ConversionMetadata) appendPlugin(index int, location string, source *Source, targets []TargetSource) {
	if source != nil {
		m.Plugins = append(m.Plugins, generatedEntitySource(index, location, source))
	}
	for targetIndex, targetSource := range targets {
		labels := make([]string, len(targetSource.Capabilities))
		for i, capability := range targetSource.Capabilities {
			labels[i] = aimap.CapabilityLabel(capability)
		}
		m.PluginTargets = append(m.PluginTargets, PluginTargetSource{
			PluginIndex:      index,
			Location:         location,
			TargetIndex:      targetIndex,
			ModelName:        targetSource.ModelName,
			ModelTargetIndex: targetSource.ModelTargetIndex,
			Capabilities:     targetSource.Capabilities,
			CapabilityLabels: labels,
		})
	}
}

func generatedEntitySource(index int, location string, source *Source) GeneratedEntitySource {
	return GeneratedEntitySource{
		Index:         index,
		Location:      location,
		EntityType:    source.EntityType,
		EntityName:    source.EntityName,
		FieldPrefix:   source.FieldPrefix,
		FieldMappings: slices.Clone(source.FieldMappings),
	}
}
