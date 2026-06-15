package convert

import "github.com/Kong/ai-deck-converter/internal/kong"

// convertVaults translates AI Gateway vaults into Kong vault entities. The
// source vault `name` becomes the Kong reference `prefix`; the source `type`
// becomes the Kong backend `name`.
func (c *Converter) convertVaults() {
	for i := range c.src.Vaults {
		v := &c.src.Vaults[i]
		c.out.Vaults = append(c.out.Vaults, kong.Vault{
			ID:          v.ID,
			Prefix:      v.Name,
			Name:        v.Type,
			Description: v.Description,
			Config:      v.Config,
			Tags:        c.labelsToTags(v.Labels),
		})
	}
}
