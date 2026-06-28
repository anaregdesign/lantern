package llm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var (
	timeType       = reflect.TypeOf(time.Time{})
	rawMessageType = reflect.TypeFor[json.RawMessage]()
)

// reflectSchema builds a JSON Schema document (as a map ready for json.Marshal)
// describing t. seen guards against recursive types.
func reflectSchema(t reflect.Type, seen map[reflect.Type]bool) (map[string]any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}, nil
	case rawMessageType:
		return map[string]any{}, nil // arbitrary JSON
	}

	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 { // []byte marshals to a base64 string
			return map[string]any{"type": "string", "contentEncoding": "base64"}, nil
		}
		return arraySchema(t, seen)
	case reflect.Array:
		return arraySchema(t, seen)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("llm: map key type %s is not a string", t.Key())
		}
		values, err := reflectSchema(t.Elem(), seen)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": values}, nil
	case reflect.Struct:
		return structSchema(t, seen)
	case reflect.Interface:
		return map[string]any{}, nil // arbitrary JSON
	default:
		return nil, fmt.Errorf("llm: unsupported type %s (kind %s)", t, t.Kind())
	}
}

func arraySchema(t reflect.Type, seen map[reflect.Type]bool) (map[string]any, error) {
	items, err := reflectSchema(t.Elem(), seen)
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": "array", "items": items}, nil
}

func structSchema(t reflect.Type, seen map[reflect.Type]bool) (map[string]any, error) {
	if seen[t] {
		return nil, fmt.Errorf("llm: recursive type %s is not supported", t)
	}
	seen[t] = true
	defer delete(seen, t)

	properties := map[string]any{}
	var required []string

	// walk handles a struct's fields, flattening anonymous embedded structs to
	// mirror how encoding/json promotes their fields.
	var walk func(reflect.Type) error
	walk = func(t reflect.Type) error {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, opts := parseJSONTag(f.Tag.Get("json"))
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				et := f.Type
				for et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				if et.Kind() == reflect.Struct {
					if err := walk(et); err != nil {
						return err
					}
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			fieldSchema, err := reflectSchema(f.Type, seen)
			if err != nil {
				return err
			}
			properties[name] = fieldSchema
			if !opts.contains("omitempty") && f.Type.Kind() != reflect.Pointer {
				required = append(required, name)
			}
		}
		return nil
	}
	if err := walk(t); err != nil {
		return nil, err
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, nil
}

// schemaName returns a JSON-schema name for t: its type name, or "output" when t
// is unnamed (e.g. an anonymous struct).
func schemaName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if name := t.Name(); name != "" {
		return name
	}
	return "output"
}

type jsonTagOptions string

// parseJSONTag splits a struct tag's `json` value into the field name and the
// comma-separated options that follow it.
func parseJSONTag(tag string) (name string, opts jsonTagOptions) {
	if i := strings.Index(tag, ","); i >= 0 {
		return tag[:i], jsonTagOptions(tag[i+1:])
	}
	return tag, ""
}

// contains reports whether opt is present in the option list.
func (o jsonTagOptions) contains(opt string) bool {
	rest := string(o)
	for rest != "" {
		var cur string
		if i := strings.Index(rest, ","); i >= 0 {
			cur, rest = rest[:i], rest[i+1:]
		} else {
			cur, rest = rest, ""
		}
		if cur == opt {
			return true
		}
	}
	return false
}
