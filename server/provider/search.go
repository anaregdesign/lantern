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

// vertexSearchDocument projects a vertex into the text that the full-text
// index analyses (#624). It folds the key together with the value so a query
// matches either the namespace path or the stored content — this mirrors the
// MCP search_facts semantics, where a remembered topic word may live in the
// key (e.g. "user.preferences.tone") or in the value, and the caller should
// not need to know which.
//
// The value side is rendered to its natural human string: text and bytes
// pass through, scalars are formatted decimally, timestamps use RFC3339 and
// durations their Go form. The nil/unset variant contributes nothing, so an
// existence-only vertex is still discoverable by its key alone.
//
// String values that hold a JSON object or array are projected to just their
// string values (#758): JSONStringValueNormalizer drops the field names and
// non-string scalars so a serialized document is searchable by its content,
// not by its structure. A JSON document carrying no string content therefore
// folds in nothing and the vertex is indexed by its key alone.
type vertexSearchProjection struct{ vertex *v1.Vertex }

func vertexSearchDocument(v *v1.Vertex) search.Document { return vertexSearchProjection{vertex: v} }

func (p vertexSearchProjection) String() string {
	if p.vertex == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(p.vertex.GetKey())
	if text, ok := vertexValueText(p.vertex); ok && text != "" {
		b.WriteByte(' ')
		b.WriteString(text)
	}
	return b.String()
}

// SizeHint is an allocation-free upper bound for the projection's UTF-8
// bytes. In particular it rejects a large JSON string before Normalize parses
// it into an object tree. Invalid UTF-8 bytes are binary and contribute zero.
func (p vertexSearchProjection) SizeHint() int {
	if p.vertex == nil {
		return 0
	}
	n := len(p.vertex.GetKey())
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
	switch val := v.GetValue().(type) {
	case *v1.Vertex_String_:
		// A string value is often a serialized JSON document; index only its
		// string values, never the JSON field names or non-string scalars
		// (#758). Non-JSON text passes through unchanged. An all-structure or
		// empty result folds in nothing (the caller drops empty value text).
		return jsonStringValueNormalizer.Normalize(val.String_), true
	case *v1.Vertex_Bytes:
		// Binary payloads are not text by accident: only valid UTF-8 participates
		// in full-text search. The projected-document byte budget is enforced by
		// the index before tokenization.
		if !utf8.Valid(val.Bytes) {
			return "", false
		}
		return string(val.Bytes), true
	case *v1.Vertex_Float64:
		return strconv.FormatFloat(val.Float64, 'g', -1, 64), true
	case *v1.Vertex_Float32:
		return strconv.FormatFloat(float64(val.Float32), 'g', -1, 32), true
	case *v1.Vertex_Int32:
		return strconv.FormatInt(int64(val.Int32), 10), true
	case *v1.Vertex_Int64:
		return strconv.FormatInt(val.Int64, 10), true
	case *v1.Vertex_Uint32:
		return strconv.FormatUint(uint64(val.Uint32), 10), true
	case *v1.Vertex_Uint64:
		return strconv.FormatUint(val.Uint64, 10), true
	case *v1.Vertex_Bool:
		return strconv.FormatBool(val.Bool), true
	case *v1.Vertex_Timestamp:
		return val.Timestamp.AsTime().Format(time.RFC3339Nano), true
	case *v1.Vertex_Duration:
		return val.Duration.AsDuration().String(), true
	default:
		// Vertex_Nil and the unset oneof: key-only indexing.
		return "", false
	}
}
