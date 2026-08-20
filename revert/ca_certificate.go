package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/Kong/ai-deck-converter/internal/aimap"
)

// revertCACertificates translates Kong CA certificate entities back into AI
// Gateway CA certificates. The name-preservation tag written by
// convertCACertificates is decoded back into name; any remaining tags become
// labels.
func (r *Reverter) revertCACertificates() {
	for i := range r.src.CACertificates {
		cert := &r.src.CACertificates[i]
		name, rest := aimap.DecodeCACertificateName(cert.Tags)
		r.out.CACertificates = append(r.out.CACertificates, aigw.CACertificate{
			Name:       name,
			Cert:       cert.Cert,
			CertDigest: cert.CertDigest,
			Labels:     r.tagsToLabels(rest),
		})
	}
}
