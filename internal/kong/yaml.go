package kong

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// yamlIndent matches the conventional Kong and decK layout.
const yamlIndent = 2

// ToYAML encodes the decK document as YAML.
func (d *Document) ToYAML() ([]byte, error) {
	return marshalYAML(d)
}

// ToYAML encodes the db-less document as YAML.
func (d *DBLessDocument) ToYAML() ([]byte, error) {
	return marshalYAML(d)
}

func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
