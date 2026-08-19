package revert

import (
	"fmt"

	"github.com/Kong/ai-deck-converter/internal/aigw"
)

// revertCertificates translates Kong certificate entities back into AI Gateway
// certificates. Kong certificates have no name, so one is synthesized from the
// position in the document -- the same approach used for providers that cannot
// be named from their config. The name is not present in the decK output, so a
// synthesized name never changes the re-converted result.
func (r *Reverter) revertCertificates() {
	for i := range r.src.Certificates {
		cert := &r.src.Certificates[i]
		r.out.Certificates = append(r.out.Certificates, aigw.Certificate{
			Name:    fmt.Sprintf("certificate-%d", i+1),
			Cert:    cert.Cert,
			Key:     cert.Key,
			CertAlt: cert.CertAlt,
			KeyAlt:  cert.KeyAlt,
			Labels:  r.tagsToLabels(cert.Tags),
		})
	}
}
