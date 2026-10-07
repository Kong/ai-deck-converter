package convert

import "github.com/Kong/ai-deck-converter/internal/kong"

// convertCertificates translates AI Gateway certificates into Kong certificate
// entities. The mapping is a straight pass-through of the PEM material; the
// source `name` has no Kong counterpart (Kong certificates are identified by id
// alone) so it is carried on SourceName for db-less ID derivation only and does
// not appear in the decK output.
func (c *Converter) convertCertificates() {
	for i := range c.src.Certificates {
		cert := &c.src.Certificates[i]
		c.out.Certificates = append(c.out.Certificates, kong.Certificate{
			ID:         cert.ID,
			Cert:       cert.Cert,
			Key:        cert.Key,
			CertAlt:    cert.CertAlt,
			KeyAlt:     cert.KeyAlt,
			Tags:       c.labelsToTags(cert.Labels),
			SourceName: cert.Name,
		})
	}
}
