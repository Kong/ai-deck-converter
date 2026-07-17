package revert

import (
	"fmt"
	"reflect"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// registerIdentityProvider dedupes a key-auth/openid-connect plugin into the
// identity provider registry: a plugin with the same type and config (minus
// the synthesized "anonymous" fallback, which convert always adds) reuses the
// existing identity provider; otherwise a new one is registered under a
// unique name.
func (r *Reverter) registerIdentityProvider(p kong.Plugin) *aigw.IdentityProvider {
	cfg := stripAnonymous(p.Config)
	if p.Name == "openid-connect" {
		cfg = r.reshapeOpenIDConnect(cfg)
	}

	for i := range r.identityProviders {
		existing := &r.identityProviders[i]
		if existing.Type != p.Name {
			continue
		}
		if !reflect.DeepEqual(existing.Config, cfg) {
			continue
		}
		return existing
	}

	idp := aigw.IdentityProvider{
		Type:   p.Name,
		Name:   r.uniqueIdentityProviderName(p.Name),
		Config: cfg,
	}
	r.identityProviders = append(r.identityProviders, idp)
	return &r.identityProviders[len(r.identityProviders)-1]
}

// uniqueIdentityProviderName derives a stable, human-readable name of the
// form "<type>-<n>".
func (r *Reverter) uniqueIdentityProviderName(idpType string) string {
	var name string
	for {
		r.identityProviderCounts[idpType]++
		candidate := fmt.Sprintf("%s-%d", idpType, r.identityProviderCounts[idpType])
		if !r.identityProviderNames[candidate] && !r.usedNames[candidate] {
			name = candidate
			break
		}
	}
	r.identityProviderNames[name] = true
	r.usedNames[name] = true
	return name
}

// stripAnonymous returns a copy of config without the "anonymous" key, which
// convert always synthesizes and which therefore carries no source-of-truth
// information for the identity provider itself.
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
