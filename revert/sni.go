package revert

import (
	"fmt"

	"github.com/Kong/ai-deck-converter/internal/aigw"
)

// revertSNIs lifts each certificate's nested Kong SNIs back into standalone
// AI Gateway SNI entities. Kong SNIs have no name distinct from their
// hostname and certificates have no name at all, so both an SNI name and its
// certificate reference are synthesized positionally, mirroring
// revertCertificates -- certificateName keeps the two in lockstep so the
// reference always matches the certificate reverted from the same index.
func (r *Reverter) revertSNIs() {
	n := 0
	for i := range r.src.Certificates {
		cert := &r.src.Certificates[i]
		for _, sni := range cert.SNIs {
			n++
			r.out.SNIs = append(r.out.SNIs, aigw.SNI{
				Name:        fmt.Sprintf("sni-%d", n),
				Hostname:    sni.Name,
				Certificate: certificateName(i),
				Labels:      r.tagsToLabels(sni.Tags),
			})
		}
	}
}
