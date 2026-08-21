package convert

import (
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// convertCACertificates translates AI Gateway CA certificates into Kong CA
// certificate entities. Only the dataplane-visible fields (cert, cert_digest)
// carry over; description and labels are control-plane-only. Kong's
// ca_certificates entity has no name field, so the source name is preserved
// as a tag for revert to recover.
func (c *Converter) convertCACertificates() {
	for i := range c.src.CACertificates {
		cert := &c.src.CACertificates[i]
		tags := c.labelsToTags(cert.Labels)
		if cert.Name != "" {
			tags = append([]string{aimap.EncodeCACertificateName(cert.Name)}, tags...)
		}
		c.out.CACertificates = append(c.out.CACertificates, kong.CACertificate{
			ID:         cert.ID,
			Cert:       cert.Cert,
			CertDigest: cert.CertDigest,
			Tags:       tags,
		})
	}
}
