package llm

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestReflectSchema_Scalars(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want map[string]any
	}{
		{"bool", reflect.TypeFor[bool](), map[string]any{"type": "boolean"}},
		{"int", reflect.TypeFor[int](), map[string]any{"type": "integer"}},
		{"uint", reflect.TypeFor[uint32](), map[string]any{"type": "integer"}},
		{"float", reflect.TypeFor[float64](), map[string]any{"type": "number"}},
		{"string", reflect.TypeFor[string](), map[string]any{"type": "string"}},
		{"bytes", reflect.TypeFor[[]byte](), map[string]any{"type": "string", "contentEncoding": "base64"}},
		{"slice", reflect.TypeFor[[]string](), map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		{"map", reflect.TypeFor[map[string]int](), map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}},
		{"pointer unwraps", reflect.TypeFor[*string](), map[string]any{"type": "string"}},
		{"time", reflect.TypeFor[time.Time](), map[string]any{"type": "string", "format": "date-time"}},
		{"raw json", reflect.TypeFor[json.RawMessage](), map[string]any{}},
		{"any", reflect.TypeFor[any](), map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reflectSchema(tc.typ, map[reflect.Type]bool{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("reflectSchema(%s) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

func TestReflectSchema_Struct(t *testing.T) {
	// Sanity-touch the unexported field so it is genuinely part of the fixture.
	if p := (profile{note: "x"}); p.note != "x" {
		t.Fatal("fixture sanity check failed")
	}

	got, err := reflectSchema(reflect.TypeFor[profile](), map[reflect.Type]bool{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	props, _ := got["properties"].(map[string]any)
	// json:"-" and unexported fields are skipped.
	if _, ok := props["Ignored"]; ok {
		t.Error(`Ignored (json:"-") leaked into properties`)
	}
	if _, ok := props["note"]; ok {
		t.Error("unexported note leaked into properties")
	}
	for _, name := range []string{"person", "emails", "address", "tags"} {
		if _, ok := props[name]; !ok {
			t.Errorf("properties missing %q: %v", name, props)
		}
	}

	// required: non-pointer, non-omitempty fields only; the *address pointer is
	// optional.
	required := toStringSet(got["required"])
	for _, name := range []string{"person", "emails", "tags"} {
		if !required[name] {
			t.Errorf("required missing %q: %v", name, got["required"])
		}
	}
	if required["address"] {
		t.Error("pointer field address must not be required")
	}
}

func TestReflectSchema_OmitemptyOptional(t *testing.T) {
	got, err := reflectSchema(reflect.TypeFor[address](), map[reflect.Type]bool{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	required := toStringSet(got["required"])
	if !required["city"] {
		t.Error("city must be required")
	}
	if required["zip"] {
		t.Error("zip (omitempty) must not be required")
	}
}

func TestReflectSchema_Errors(t *testing.T) {
	t.Run("recursive type", func(t *testing.T) {
		type node struct {
			Next *node `json:"next"`
		}
		if _, err := reflectSchema(reflect.TypeFor[node](), map[reflect.Type]bool{}); err == nil {
			t.Error("expected error for recursive type, got nil")
		}
	})

	t.Run("non-string map key", func(t *testing.T) {
		if _, err := reflectSchema(reflect.TypeFor[map[int]string](), map[reflect.Type]bool{}); err == nil {
			t.Error("expected error for non-string map key, got nil")
		}
	})
}

func toStringSet(v any) map[string]bool {
	set := map[string]bool{}
	items, _ := v.([]string)
	for _, s := range items {
		set[s] = true
	}
	return set
}
