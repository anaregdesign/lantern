package llm

import (
	"encoding/json"
	"testing"
)

func TestSchemaFor(t *testing.T) {
	t.Run("derives name, strictness, and an object schema from a struct", func(t *testing.T) {
		s, err := SchemaFor[person]()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Name != "person" {
			t.Errorf("Name = %q, want person", s.Name)
		}
		if !s.Strict {
			t.Error("Strict = false, want true")
		}

		var doc map[string]any
		if err := json.Unmarshal(s.Definition, &doc); err != nil {
			t.Fatalf("Definition is not valid JSON: %v", err)
		}
		if doc["type"] != "object" {
			t.Errorf("type = %v, want object", doc["type"])
		}
		if doc["additionalProperties"] != false {
			t.Errorf("additionalProperties = %v, want false", doc["additionalProperties"])
		}
		props, _ := doc["properties"].(map[string]any)
		for _, name := range []string{"name", "age"} {
			if _, ok := props[name]; !ok {
				t.Errorf("properties missing %q: %v", name, props)
			}
		}
	})

	t.Run("names an unnamed type output", func(t *testing.T) {
		s, err := SchemaFor[struct {
			OK bool `json:"ok"`
		}]()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Name != "output" {
			t.Errorf("Name = %q, want output", s.Name)
		}
	})

	t.Run("rejects unsupported types", func(t *testing.T) {
		if _, err := SchemaFor[chan int](); err == nil {
			t.Error("expected error for chan int, got nil")
		}
	})
}
