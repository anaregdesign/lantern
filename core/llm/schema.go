package llm

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Schema is a backend-agnostic description of the structure a Model's output
// must conform to. It is expressed as a JSON Schema document — the interchange
// format understood by every supported backend (OpenAI structured outputs,
// Gemini response schemas, Claude tool input schemas, ...). The document is a
// canonical, strict-leaning form (objects pin additionalProperties to false and
// list every non-optional field in required); each backend translates it into
// the subset its provider accepts.
//
// Name identifies the schema; some backends require a non-empty name for a
// structured-output request. Description is an optional human-readable summary.
// Definition is the JSON Schema document itself. When Strict is true the output
// must validate against Definition exactly; backends that always enforce their
// response schema may treat the flag as implied.
type Schema struct {
	Name        string
	Description string
	Definition  json.RawMessage
	Strict      bool
}

// SchemaFor derives a strict Schema from the Go type T by reflection, so a
// caller describes the desired output as an ordinary struct rather than by
// hand-writing JSON Schema. Name defaults to T's type name ("output" when T is
// unnamed); set Name or Description on the result to override.
//
// The supported subset covers what structured-output backends accept: structs
// (honoring json tags, with embedded structs flattened and json:"-" fields
// skipped), bool, the integer and float kinds, string, []byte (as a base64
// string), slices and arrays, string-keyed maps, pointers (which mark a field
// optional), and time.Time (as a date-time string). A non-optional field — one
// without omitempty and not a pointer — is added to the object's required list.
// SchemaFor returns an error for types it cannot represent (channels, funcs,
// non-string map keys) and for recursive types.
func SchemaFor[T any]() (Schema, error) {
	t := reflect.TypeFor[T]()
	def, err := reflectSchema(t, map[reflect.Type]bool{})
	if err != nil {
		return Schema{}, err
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return Schema{}, fmt.Errorf("llm: marshal JSON schema for %s: %w", t, err)
	}
	return Schema{
		Name:       schemaName(t),
		Definition: raw,
		Strict:     true,
	}, nil
}
