package convert

import "github.com/Kong/ai-deck-converter/internal/kong"

// convertSNIs nests each AI Gateway SNI under the Kong certificate its
// `certificate` field names. decK's file format has no standalone SNI entity,
// so the AI Gateway SNI's own name/display_name have no Kong counterpart and
// are dropped -- Name becomes the nested entry's `name` (Kong's SNI.Name
// doubles as the hostname).
func (c *Converter) convertSNIs() error {
	certIndex := make(map[string]int, len(c.src.Certificates))
	for i, cert := range c.src.Certificates {
		certIndex[cert.Name] = i
	}
	for _, sni := range c.src.SNIs {
		idx, ok := certIndex[sni.Certificate]
		if !ok {
			if err := c.warn("sni %q references unknown certificate %q", sni.Name, sni.Certificate); err != nil {
				return err
			}
			continue
		}
		c.out.Certificates[idx].SNIs = append(c.out.Certificates[idx].SNIs, kong.SNI{
			ID:   sni.ID,
			Name: sni.Hostname,
			Tags: c.labelsToTags(sni.Labels),
		})
	}
	return nil
}
