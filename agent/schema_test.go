package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// schemaPath is the schema this repository owns. The API serves it at
// /api/v1/schemas/agent-config.json by reading it from here, the same way it
// serves the built binaries.
const schemaPath = "schemas/agent-config.json"

func loadSchema(t *testing.T) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", schemaPath, err)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("the published schema is not valid JSON: %v", err)
	}
	return schema
}

// collectProperties gathers every property name the schema declares, at any
// depth. A field only has to be documented somewhere, not in one exact place.
func collectProperties(node interface{}, into map[string]bool) {
	switch value := node.(type) {
	case map[string]interface{}:
		if properties, ok := value["properties"].(map[string]interface{}); ok {
			for name := range properties {
				into[name] = true
			}
		}
		for _, child := range value {
			collectProperties(child, into)
		}
	case []interface{}:
		for _, child := range value {
			collectProperties(child, into)
		}
	}
}

// yamlFields lists the yaml tags of a struct, following nested structs.
func yamlFields(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			into[tag] = true
		}
		yamlFields(field.Type, into)
	}
}

// The schema is what an editor validates a config against. A field the agent
// reads but the schema does not know is reported to the operator as an error in
// a config that actually works.
func TestPublishedSchemaKnowsEveryConfigField(t *testing.T) {
	schema := loadSchema(t)

	documented := map[string]bool{}
	collectProperties(schema, documented)

	fields := map[string]bool{}
	yamlFields(reflect.TypeOf(AgentConfig{}), fields)

	var missing []string
	for field := range fields {
		if !documented[field] {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s does not document the config fields %v", schemaPath, missing)
	}
}

// The example file is what an operator copies. It must satisfy the same
// validation the agent runs at startup, or the first thing they do is fix it.
func TestExampleConfigIsValid(t *testing.T) {
	if _, err := os.Stat("config.yaml.example"); err != nil {
		t.Skipf("no example config: %v", err)
	}
	if _, err := loadConfig("config.yaml.example"); err != nil {
		t.Fatalf("config.yaml.example does not load: %v", err)
	}
}
