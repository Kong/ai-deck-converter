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
	cfg := stripSyntheticAuthFields(p.Name, p.Config)

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

// syntheticAuthFields lists, per auth plugin type, the config keys that never
// belong on the AI Gateway auth strategy: they are synthesized by the forward
// converter or injected by the data plane, not authored by the user.
//   - anonymous: convert always sets it as the fallback consumer marker.
//   - identity_realms / realm: DP-defaulted on key-auth (convert re-emits
//     identity_realms via forceKeyAuthIdentityRealms when principals is
//     enabled, so stripping it here still round-trips byte-identically).
var syntheticAuthFields = map[string][]string{
	"key-auth":       {"anonymous", "identity_realms", "realm"},
	"openid-connect": {"anonymous"},
}

// stripSyntheticAuthFields returns a copy of an auth plugin's config with the
// synthetic/DP-injected keys for its type removed, so they do not surface on
// the reconstructed AI Gateway auth strategy.
func stripSyntheticAuthFields(pluginName string, config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	drop := make(map[string]bool, 3)
	for _, k := range syntheticAuthFields[pluginName] {
		drop[k] = true
	}
	out := make(map[string]any, len(config))
	for k, v := range config {
		if drop[k] {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
