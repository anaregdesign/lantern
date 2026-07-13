package provider

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/core/search"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
)

// jsonStringValueNormalizer extracts only the string values from a JSON
// object/array value, dropping field names and non-string scalars, and passes
// non-JSON text through unchanged (#758). It is stateless and safe to share.
var jsonStringValueNormalizer search.JSONStringValueNormalizer

// vertexSearchDocument projects a vertex into explicit key and value fields.
// A query still matches either namespace path or stored content, but BM25,
// phrase, and proximity evidence never cross their synthetic boundary (#1061).
//
// The value side is rendered to its natural human string: text and bytes
// pass through, scalars are formatted decimally, timestamps use RFC3339 and
// durations their Go form. The nil/unset variant contributes nothing, so an
// existence-only vertex is still discoverable by its key alone.
//
// String values that hold a JSON object or array are projected to just their
// string values (#758): JSONStringValueNormalizer drops field names and
// non-string scalars, and each string leaf remains a separate value-field
// instance. A JSON document carrying no string content is key-only.
type vertexSearchProjection struct {
	key    string
	vertex *v1.Vertex
}

func vertexSearchDocument(key string, v *v1.Vertex) search.Document {
	return vertexSearchProjection{key: key, vertex: v}
}

func (p vertexSearchProjection) SearchFields() []search.DocumentField {
	fields := make([]search.DocumentField, 0, 2)
	if p.key != "" {
		fields = append(fields, search.DocumentField{ID: search.FieldKey, Text: p.key})
	}
	for _, text := range vertexValueTexts(p.vertex) {
		if text != "" {
			fields = append(fields, search.DocumentField{ID: search.FieldValue, Text: text})
		}
	}
	return fields
}

func (p vertexSearchProjection) String() string {
	var b strings.Builder
	for _, field := range p.SearchFields() {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(field.Text)
	}
	return b.String()
}

// SizeHint is an allocation-free upper bound for the projection's UTF-8
// bytes. In particular it rejects a large JSON string before Normalize parses
// it into an object tree. Invalid UTF-8 bytes are binary and contribute zero.
func (p vertexSearchProjection) SizeHint() int {
	if p.vertex == nil {
		return len(p.key)
	}
	n := len(p.key)
	var valueBytes int
	switch value := p.vertex.GetValue().(type) {
	case *v1.Vertex_String_:
		valueBytes = len(value.String_)
	case *v1.Vertex_Bytes:
		if utf8.Valid(value.Bytes) {
			valueBytes = len(value.Bytes)
		}
	default:
		if text, ok := vertexValueText(p.vertex); ok {
			valueBytes = len(text)
		}
	}
	if valueBytes > 0 {
		n += 1 + valueBytes
	}
	return n
}

// vertexValueText returns the indexable text form of a vertex's value and
// whether the value carries any text at all. The unset and explicit-nil
// variants return ("", false) so callers index the key only.
func vertexValueText(v *v1.Vertex) (string, bool) {
	values := vertexValueTexts(v)
	return strings.Join(values, " "), len(values) > 0
}

func vertexValueTexts(v *v1.Vertex) []string {
	if v == nil {
		return nil
	}
	switch val := v.GetValue().(type) {
	case *v1.Vertex_String_:
		// A string value is often a serialized JSON document; index only its
		// string values, never the JSON field names or non-string scalars
		// (#758). Non-JSON text passes through unchanged. An all-structure or
		// empty result folds in nothing (the caller drops empty value text).
		if values, structured := jsonStringValueNormalizer.Values(val.String_); structured {
			return values
		}
		return []string{val.String_}
	case *v1.Vertex_Bytes:
		// Binary payloads are not text by accident: only valid UTF-8 participates
		// in full-text search. The projected-document byte budget is enforced by
		// the index before tokenization.
		if !utf8.Valid(val.Bytes) {
			return nil
		}
		return []string{string(val.Bytes)}
	case *v1.Vertex_Float64:
		return []string{strconv.FormatFloat(val.Float64, 'g', -1, 64)}
	case *v1.Vertex_Float32:
		return []string{strconv.FormatFloat(float64(val.Float32), 'g', -1, 32)}
	case *v1.Vertex_Int32:
		return []string{strconv.FormatInt(int64(val.Int32), 10)}
	case *v1.Vertex_Int64:
		return []string{strconv.FormatInt(val.Int64, 10)}
	case *v1.Vertex_Uint32:
		return []string{strconv.FormatUint(uint64(val.Uint32), 10)}
	case *v1.Vertex_Uint64:
		return []string{strconv.FormatUint(val.Uint64, 10)}
	case *v1.Vertex_Bool:
		return []string{strconv.FormatBool(val.Bool)}
	case *v1.Vertex_Timestamp:
		return []string{val.Timestamp.AsTime().Format(time.RFC3339Nano)}
	case *v1.Vertex_Duration:
		return []string{val.Duration.AsDuration().String()}
	default:
		// Vertex_Nil and the unset oneof: key-only indexing.
		return nil
	}
}
