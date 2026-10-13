package revert

import (
	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// upstreamTLS changes the certificate IDs on a Service back to the names that
// revertCertificates and revertCACertificates give.
func (r *Reverter) upstreamTLS(owner string, svc *kong.Service) (certs, caCerts []string, err error) {
	if id := svc.ClientCertificate["id"]; id != "" {
		name, ok := r.certificateNameByID(id)
		if ok {
			certs = []string{name}
		} else if err := r.warn("%s references unknown certificate id %q", owner, id); err != nil {
			return nil, nil, err
		}
	}
	for _, id := range svc.CACertificates {
		name, ok := r.caCertificateNameByID(id)
		if !ok {
			if err := r.warn("%s references unknown CA certificate id %q", owner, id); err != nil {
				return nil, nil, err
			}
			continue
		}
		caCerts = append(caCerts, name)
	}
	return certs, caCerts, nil
}

func (r *Reverter) certificateNameByID(id string) (string, bool) {
	for i := range r.src.Certificates {
		if r.src.Certificates[i].ID == id {
			return certificateName(i), true
		}
	}
	return "", false
}

func (r *Reverter) caCertificateNameByID(id string) (string, bool) {
	for i := range r.src.CACertificates {
		if r.src.CACertificates[i].ID != id {
			continue
		}
		name, _ := aimap.DecodeCACertificateName(r.src.CACertificates[i].Tags)
		return name, name != ""
	}
	return "", false
}
