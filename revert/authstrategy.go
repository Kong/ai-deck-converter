package revert

import (
	"fmt"
	"reflect"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// registerAuthStrategy dedupes a key-auth/openid-connect plugin into the auth
// strategy registry: a plugin with the same type and config (minus the
// synthesized "anonymous" fallback, which convert always adds) reuses the
// existing auth strategy; otherwise a new one is registered under a unique
// name.
func (r *Reverter) registerAuthStrategy(p kong.Plugin) *aigw.AuthStrategy {
	cfg := stripAnonymous(p.Config)

	for i := range r.authStrategies {
		existing := &r.authStrategies[i]
		if existing.Type != p.Name {
			continue
		}
		if !reflect.DeepEqual(existing.Config, cfg) {
			continue
		}
		return existing
	}

	idp := aigw.AuthStrategy{
		Type:   p.Name,
		Name:   r.uniqueAuthStrategyName(p.Name),
		Config: cfg,
	}
	r.authStrategies = append(r.authStrategies, idp)
	return &r.authStrategies[len(r.authStrategies)-1]
}

// uniqueAuthStrategyName derives a stable, human-readable name of the
// form "<type>-<n>".
func (r *Reverter) uniqueAuthStrategyName(idpType string) string {
	var name string
	for {
		r.authStrategyCounts[idpType]++
		candidate := fmt.Sprintf("%s-%d", idpType, r.authStrategyCounts[idpType])
		if !r.authStrategyNames[candidate] {
			name = candidate
			break
		}
	}
	r.authStrategyNames[name] = true
	return name
}

// stripAnonymous returns a copy of config without the "anonymous" key, which
// convert always synthesizes and which therefore carries no source-of-truth
// information for the auth strategy itself.
func stripAnonymous(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	out := make(map[string]any, len(config))
	for k, v := range config {
		if k == "anonymous" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
