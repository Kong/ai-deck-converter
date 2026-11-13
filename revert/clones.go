package revert

import "github.com/Kong/ai-deck-converter/internal/aimap"

// warnClonedPlugins reports the clone definitions the AI Gateway model cannot
// express. A policy reverts as the plugin its clone copies, so the clone itself
// — its name and any priority — is dropped.
//
// A converter-emitted clone is the exception: it exists only to run the policy
// after ai-proxy-advanced, at the converter's own priority. A clone is only
// treated as that when its ref and priority match, since a clone the converter
// did not emit would be rewritten by the next conversion.
func (r *Reverter) warnClonedPlugins() error {
	for _, clone := range r.src.ClonedPlugins {
		ref, emitted := aimap.CloneRef(clone.Name)
		if !emitted {
			if err := r.warn(
				"cloned plugin %q (ref %q) has no AI Gateway representation; its name and priority "+
					"are dropped, and a policy using it reverts as type %q",
				clone.Name, clone.Ref, clone.Ref); err != nil {
				return err
			}
			continue
		}
		priority := 0
		if clone.Priority != nil {
			priority = *clone.Priority
		}
		if clone.Ref != ref || priority != aimap.ResponseTransformerClonePriority {
			if err := r.warn(
				"cloned plugin %q sets ref %q and priority %d; converting again emits ref %q and "+
					"priority %d",
				clone.Name, clone.Ref, priority, ref, aimap.ResponseTransformerClonePriority); err != nil {
				return err
			}
		}
	}
	return nil
}
