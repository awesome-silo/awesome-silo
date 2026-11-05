// Package yamlschema validates YAML documents against a JSON schema.
package yamlschema

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// Validate parses the YAML file at path and checks it against schema.
func Validate(schema *jsonschema.Schema, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var document any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse YAML %s: %w", path, err)
	}
	jsonDocument, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("convert %s to JSON for schema validation: %w", path, err)
	}
	var value any
	if err := json.Unmarshal(jsonDocument, &value); err != nil {
		return fmt.Errorf("decode JSON for %s: %w", path, err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}
	return nil
}
