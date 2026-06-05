package revert

import "github.com/gperanich/ai-deck-converter/internal/aigw"

// revertVaults translates Kong vault entities back into AI Gateway vaults. The
// Kong reference `prefix` becomes the source vault `name`; the Kong backend
// `name` becomes the source `type`.
func (r *Reverter) revertVaults() {
	for i := range r.src.Vaults {
		v := &r.src.Vaults[i]
		r.out.Vaults = append(r.out.Vaults, aigw.Vault{
			Type:        v.Name,
			Name:        v.Prefix,
			Description: v.Description,
			Config:      v.Config,
			Labels:      r.tagsToLabels(v.Tags),
		})
	}
}
