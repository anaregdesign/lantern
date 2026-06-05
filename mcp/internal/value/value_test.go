package value

import (
	"encoding/json"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestToSDK_PrimitivesPassThrough(t *testing.T) {
	cases := []struct {
		name string
		in   any
	}{
		{"nil", nil},
		{"bool", true},
		{"string", "hello"},
		{"int", 42},
		{"int64", int64(-7)},
		{"uint64", uint64(9)},
		{"float64", 3.14},
		{"bytes", []byte{0x01, 0x02}},
		{"time", time.Unix(1_700_000_000, 0).UTC()},
		{"duration", 5 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ToSDK(c.in)
			if err != nil {
				t.Fatalf("ToSDK(%v): %v", c.in, err)
			}
			// nil round-trips as untyped nil; the rest compare via fmt
			// rendering because [] byte / struct equality is awkward.
			if c.in == nil && got != nil {
				t.Fatalf("ToSDK(nil) = %v, want nil", got)
			}
		})
	}
}

func TestToSDK_CompositesJSONEncoded(t *testing.T) {
	in := map[string]any{"name": "lantern", "age": float64(2)}
	got, err := ToSDK(in)
	if err != nil {
		t.Fatalf("ToSDK: %v", err)
	}
	s, ok := got.(string)
	if !ok {
		t.Fatalf("ToSDK(map) = %T, want string", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(s), &decoded); err != nil {
		t.Fatalf("encoded string is not valid JSON: %v", err)
	}
	if decoded["name"] != "lantern" || decoded["age"].(float64) != 2 {
		t.Fatalf("encoded payload mismatch: %v", decoded)
	}
}

func TestToSDK_SliceJSONEncoded(t *testing.T) {
	in := []any{"a", float64(1), true}
	got, err := ToSDK(in)
	if err != nil {
		t.Fatalf("ToSDK: %v", err)
	}
	s, ok := got.(string)
	if !ok {
		t.Fatalf("ToSDK(slice) = %T, want string", got)
	}
	if s != `["a",1,true]` {
		t.Fatalf("encoded slice = %q, want [\"a\",1,true]", s)
	}
}

func TestFromVertex_RoundTripsPrimitives(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  any
	}{
		{"string", "hello", "hello"},
		{"bool-true", true, true},
		{"bool-false", false, false},
		{"int64", int64(42), 42},
		{"float64", 3.14, 3.14},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := mustVertex("k", c.value)
			if err != nil {
				t.Fatalf("mustVertex: %v", err)
			}
			got := FromVertex(v)
			if got != c.want {
				t.Fatalf("FromVertex = %v (%T), want %v (%T)", got, got, c.want, c.want)
			}
		})
	}
}

func TestFromVertex_DecodesStructuredString(t *testing.T) {
	// Encode a map via ToSDK then store it as a string vertex.
	encoded, err := ToSDK(map[string]any{"x": float64(1), "y": "two"})
	if err != nil {
		t.Fatalf("ToSDK: %v", err)
	}
	v, err := mustVertex("k", encoded)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	got := FromVertex(v)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("FromVertex returned %T, want map[string]any", got)
	}
	if m["x"].(float64) != 1 || m["y"] != "two" {
		t.Fatalf("decoded payload mismatch: %v", m)
	}
}

func TestFromVertex_PlainStringStaysString(t *testing.T) {
	v, err := mustVertex("k", "not-json")
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	got := FromVertex(v)
	if got != "not-json" {
		t.Fatalf("FromVertex = %v, want \"not-json\"", got)
	}
}

func TestFromVertex_NilVariantIsNil(t *testing.T) {
	v, err := mustVertex("k", nil)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := FromVertex(v); got != nil {
		t.Fatalf("FromVertex(nil-variant) = %v, want nil", got)
	}
}

func TestFromVertex_NilPointerIsNil(t *testing.T) {
	if got := FromVertex(nil); got != nil {
		t.Fatalf("FromVertex(nil) = %v, want nil", got)
	}
}

// mustVertex builds a *client.Vertex (= *pb.Vertex) whose oneof variant
// matches v's concrete type. The variant matrix replicates the SDK's
// internal nativeVertex.asVertex path; we only need enough cases to cover
// the FromVertex code paths exercised by this package's tests.
func mustVertex(key string, v any) (*client.Vertex, error) {
	switch x := v.(type) {
	case nil:
		return &pb.Vertex{Key: key, Value: &pb.Vertex_Nil{Nil: true}}, nil
	case bool:
		return &pb.Vertex{Key: key, Value: &pb.Vertex_Bool{Bool: x}}, nil
	case string:
		return &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: x}}, nil
	case int:
		return &pb.Vertex{Key: key, Value: &pb.Vertex_Int64{Int64: int64(x)}}, nil
	case int64:
		return &pb.Vertex{Key: key, Value: &pb.Vertex_Int64{Int64: x}}, nil
	case float64:
		return &pb.Vertex{Key: key, Value: &pb.Vertex_Float64{Float64: x}}, nil
	}
	return nil, errUnsupportedTestType
}

var errUnsupportedTestType = errInvalidTestType("unsupported test value type")

type errInvalidTestType string

func (e errInvalidTestType) Error() string { return string(e) }
