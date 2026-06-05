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
	"time"

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
