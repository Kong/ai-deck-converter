package revert

import "github.com/Kong/ai-deck-converter/internal/aigw"

// revertVaults translates Kong vault entities back into AI Gateway vaults. The
// Kong reference `prefix` becomes the source vault `name`; the Kong backend
// `name` becomes the source `type`.
func (r *Reverter) revertVaults() {
	for i := range r.src.Vaults {
		v := &r.src.Vaults[i]
		// Konnect config-store vaults carry an environment-specific
		// config_store_id that binds one-to-one to an existing config store and
		// is not portable across orgs/namespaces (creating a second vault for
		// the same store is rejected). Drop them; re-binding the config store is
		// a manual migration step.
		if v.Name == "konnect" {
			r.warn("vault %q (type konnect): dropped; config-store binding must be "+
				"recreated manually after migration", v.Prefix) //nolint:errcheck
			continue
		}
		r.out.Vaults = append(r.out.Vaults, aigw.Vault{
			Type:        v.Name,
			Name:        v.Prefix,
			Description: v.Description,
			Config:      v.Config,
			Labels:      r.tagsToLabels(v.Tags),
		})
	}
}
