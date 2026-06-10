// Package value bridges the dynamic JSON-shaped values that MCP tool inputs
// carry into the concrete Go types the Lantern SDK's nativeVertex.asVertex
// path accepts (and the reverse direction for tool outputs).
//
// The MCP framework decodes tool arguments via encoding/json, so an "any"
// field arrives as one of: nil, bool, float64, string, []any, or
// map[string]any. The Lantern SDK accepts primitives directly but rejects
// composite shapes (map / slice) with ErrInvalidType. To preserve the
// agent's value losslessly we JSON-encode composites into a string and
// store that string; on recall we attempt to JSON-decode the string and
// return the structured shape when it round-trips, falling back to the
// raw string otherwise.
package value

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// ToSDK converts an MCP-supplied value into the form accepted by
// client.PutVertex. Primitives pass through unchanged; composite values
// (objects, arrays) are JSON-encoded into a string so they round-trip via
// FromVertex.
func ToSDK(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case bool, string, []byte,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64,
		time.Time, time.Duration:
		return x, nil
	default:
		// map[string]any, []any, json.Number, or any other shape the JSON
		// decoder might hand us. Round-trip via JSON so FromVertex can
		// reverse the encoding without ambiguity.
		b, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("value: marshal composite: %w", err)
		}
		return string(b), nil
	}
}

// FromVertex converts a Lantern vertex back into an MCP-friendly any.
// Primitive variants are returned as their natural Go type; string
// variants are JSON-decoded when the payload parses to a structured
// (object / array) shape, otherwise returned verbatim. Timestamps and
// durations are stringified to keep the output JSON-serializable.
func FromVertex(v *client.Vertex) any {
	if v == nil {
		return nil
	}
	switch client.Kind(v) {
	case client.VertexKindNil:
		return nil
	case client.VertexKindInt32, client.VertexKindInt64:
		n, err := client.IntValue(v)
		if err != nil {
			return nil
		}
		return n
	case client.VertexKindUint32, client.VertexKindUint64:
		n, err := client.UIntValue(v)
		if err != nil {
			return nil
		}
		return n
	case client.VertexKindFloat32, client.VertexKindFloat64:
		f, err := client.FloatValue(v)
		if err != nil {
			return nil
		}
		return f
	case client.VertexKindBool:
		b, err := client.BoolValue(v)
		if err != nil {
			return nil
		}
		return b
	case client.VertexKindString:
		s, err := client.StringValue(v)
		if err != nil {
			return nil
		}
		// Try to decode as JSON; restore structured shapes that ToSDK
		// encoded. Primitive JSON (a bare string, number, bool) is not
		// promoted — the agent stored a string and should get a string
		// back.
		var decoded any
		if jerr := json.Unmarshal([]byte(s), &decoded); jerr == nil {
			switch decoded.(type) {
			case map[string]any, []any:
				return decoded
			}
		}
		return s
	case client.VertexKindBytes:
		b, err := client.BytesValue(v)
		if err != nil {
			return nil
		}
		return b
	case client.VertexKindTimestamp:
		t, err := client.TimeValue(v)
		if err != nil {
			return nil
		}
		return t.Format(time.RFC3339Nano)
	case client.VertexKindDuration:
		d, err := client.DurationValue(v)
		if err != nil {
			return nil
		}
		return d.String()
	}
	return nil
}

// Native extracts a vertex's stored value as the Go type that round-trips
// back to the SAME oneof variant when handed to client.PutVertex (via
// nativeVertex.asVertex). It exists for the value-preserving re-put that
// backs the touch tool (#543): unlike FromVertex — which stringifies
// timestamps/durations and promotes JSON strings to structured shapes for
// MCP *output* — Native preserves the stored kind and width exactly
// (Int32 stays Int32, Timestamp stays Timestamp), so touching a fact
// extends its TTL without mutating the value the server holds.
//
// A nil vertex and the Vertex_Nil tombstone both return nil, which
// re-puts as the same present-nil tombstone. Any unknown/unset variant
// also returns nil rather than erroring: the caller is re-storing a value
// the server already accepted, so there is nothing to validate.
func Native(v *client.Vertex) any {
	if v == nil {
		return nil
	}
	switch x := v.Value.(type) {
	case *pb.Vertex_Float32:
		return x.Float32
	case *pb.Vertex_Float64:
		return x.Float64
	case *pb.Vertex_Int32:
		return x.Int32
	case *pb.Vertex_Int64:
		return x.Int64
	case *pb.Vertex_Uint32:
		return x.Uint32
	case *pb.Vertex_Uint64:
		return x.Uint64
	case *pb.Vertex_Bool:
		return x.Bool
	case *pb.Vertex_String_:
		return x.String_
	case *pb.Vertex_Bytes:
		return x.Bytes
	case *pb.Vertex_Timestamp:
		return x.Timestamp.AsTime()
	case *pb.Vertex_Duration:
		return x.Duration.AsDuration()
	default:
		// Vertex_Nil tombstone or an unset/unknown variant: re-put as nil.
		return nil
	}
}

// SnippetMaxRunes bounds the truncated value preview returned by Snippet.
// It keeps namespace surveys and search results legible without spilling
// multi-KB values into the model context.
const SnippetMaxRunes = 120

// Text renders a vertex value as a single-line string with embedded
// newlines, carriage returns, and tabs collapsed to single spaces, and with
// NO length limit. Structured values are JSON-encoded. It returns "" for a
// nil vertex or a nil value. Text is the full-length searchable form used by
// substring search (search_facts); Snippet is the truncated preview built on
// top of it.
func Text(v *client.Vertex) string {
	val := FromVertex(v)
	if val == nil {
		return ""
	}
	var s string
	switch x := val.(type) {
	case string:
		s = x
	default:
		if b, err := json.Marshal(x); err == nil {
			s = string(b)
		} else {
			s = fmt.Sprintf("%v", x)
		}
	}
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
}

// Snippet renders a vertex value as a compact, single-line preview for
// namespace surveys and search results: structured values are JSON-encoded,
// embedded newlines and tabs are collapsed to spaces, and the result is
// truncated to SnippetMaxRunes runes with a trailing "…" when shortened. It
// returns "" for a nil vertex or a nil value. Callers that need the full
// value should use FromVertex instead.
func Snippet(v *client.Vertex) string {
	s := Text(v)
	if runes := []rune(s); len(runes) > SnippetMaxRunes {
		return string(runes[:SnippetMaxRunes]) + "…"
	}
	return s
}
