package convert

import (
	"fmt"
	"net/url"

	"github.com/Kong/ai-deck-converter/internal/kong"
)

// applyUpstreamTLS sets the client certificate and CA certificates of the
// Service that an MCP server or agent generates. A referenced certificate
// without an ID gets the ID that the db-less output derives for it.
func (c *Converter) applyUpstreamTLS(src ConversionIssueSource, svc *kong.Service, certs, caCerts []string) {
	if len(certs) == 0 && len(caCerts) == 0 {
		return
	}
	// Kong rejects the whole configuration if a non-https Service has these fields.
	if u, err := url.Parse(svc.URL); err != nil || u.Scheme != "https" {
		c.warn(src.at("config.certificates", "config.ca_certificates"), ConfigFieldIgnored,
			"%s %q sets certificates or ca_certificates, but its url %q is not https; "+
				"they are ignored", src.Kind, src.Name, svc.URL)
		return
	}

	if len(certs) > 0 {
		if len(certs) > 1 {
			c.warn(src.at("config.certificates"), ConfigFieldDropped,
				"%s %q sets %d certificates, but a Service presents one client certificate; "+
					"only %q is used", src.Kind, src.Name, len(certs), certs[0])
		}
		if id, ok := c.certificateID(certs[0]); ok {
			svc.ClientCertificate = map[string]string{"id": id}
		} else {
			c.warn(src.at("config.certificates[0]"), UndefinedReference,
				"%s %q references unknown certificate %q", src.Kind, src.Name, certs[0])
		}
	}

	for i, name := range caCerts {
		id, ok := c.caCertificateID(name)
		if !ok {
			c.warn(src.at(fmt.Sprintf("config.ca_certificates[%d]", i)), UndefinedReference,
				"%s %q references unknown CA certificate %q", src.Kind, src.Name, name)
			continue
		}
		svc.CACertificates = append(svc.CACertificates, id)
	}
}

func (c *Converter) certificateID(name string) (string, bool) {
	for i := range c.out.Certificates {
		cert := &c.out.Certificates[i]
		if cert.SourceName != name {
			continue
		}
		if cert.ID == "" {
			cert.ID = kong.StableUUID("certificate:" + name)
		}
		return cert.ID, true
	}
	return "", false
}

// caCertificateID relies on convertCACertificates keeping the source order.
func (c *Converter) caCertificateID(name string) (string, bool) {
	for i := range c.src.CACertificates {
		if c.src.CACertificates[i].Name != name {
			continue
		}
		cert := &c.out.CACertificates[i]
		if cert.ID == "" {
			cert.ID = kong.StableUUID("ca_certificate:" + cert.Cert)
		}
		return cert.ID, true
	}
	return "", false
}
