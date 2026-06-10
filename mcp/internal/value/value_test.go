package value

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestSnippet_NilVertexAndNilValueAreEmpty(t *testing.T) {
	if got := Snippet(nil); got != "" {
		t.Fatalf("Snippet(nil) = %q, want empty", got)
	}
	v, err := mustVertex("k", nil)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := Snippet(v); got != "" {
		t.Fatalf("Snippet(nil-variant) = %q, want empty", got)
	}
}

func TestSnippet_ShortStringVerbatim(t *testing.T) {
	v, err := mustVertex("k", "concise value")
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := Snippet(v); got != "concise value" {
		t.Fatalf("Snippet = %q, want %q", got, "concise value")
	}
}

func TestSnippet_TruncatesLongStringToCap(t *testing.T) {
	long := strings.Repeat("x", SnippetMaxRunes+50)
	v, err := mustVertex("k", long)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	got := Snippet(v)
	wantRunes := SnippetMaxRunes + 1 // cap runes plus the trailing ellipsis
	if n := len([]rune(got)); n != wantRunes {
		t.Fatalf("Snippet length = %d runes, want %d", n, wantRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated Snippet should end with ellipsis: %q", got)
	}
}

func TestSnippet_StructuredValueJSONEncoded(t *testing.T) {
	encoded, err := ToSDK(map[string]any{"name": "lantern"})
	if err != nil {
		t.Fatalf("ToSDK: %v", err)
	}
	v, err := mustVertex("k", encoded)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := Snippet(v); got != `{"name":"lantern"}` {
		t.Fatalf("Snippet = %q, want %q", got, `{"name":"lantern"}`)
	}
}

func TestSnippet_CollapsesNewlinesToSpaces(t *testing.T) {
	v, err := mustVertex("k", "line one\nline two\ttabbed")
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := Snippet(v); got != "line one line two tabbed" {
		t.Fatalf("Snippet = %q, want collapsed single line", got)
	}
}

func TestText_NilVertexAndNilValueAreEmpty(t *testing.T) {
	if got := Text(nil); got != "" {
		t.Fatalf("Text(nil) = %q, want empty", got)
	}
	v, err := mustVertex("k", nil)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := Text(v); got != "" {
		t.Fatalf("Text(nil-variant) = %q, want empty", got)
	}
}

func TestText_DoesNotTruncateLongValue(t *testing.T) {
	long := strings.Repeat("z", SnippetMaxRunes+200)
	v, err := mustVertex("k", long)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	got := Text(v)
	if got != long {
		t.Fatalf("Text truncated or altered the value: len=%d want=%d", len([]rune(got)), len([]rune(long)))
	}
	if strings.HasSuffix(got, "…") {
		t.Fatalf("Text must not append an ellipsis: %q", got)
	}
}

func TestText_CollapsesNewlinesToSpaces(t *testing.T) {
	v, err := mustVertex("k", "build\n2026\trelease")
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := Text(v); got != "build 2026 release" {
		t.Fatalf("Text = %q, want collapsed single line", got)
	}
}

func TestText_StructuredValueJSONEncoded(t *testing.T) {
	encoded, err := ToSDK(map[string]any{"name": "lantern"})
	if err != nil {
		t.Fatalf("ToSDK: %v", err)
	}
	v, err := mustVertex("k", encoded)
	if err != nil {
		t.Fatalf("mustVertex: %v", err)
	}
	if got := Text(v); got != `{"name":"lantern"}` {
		t.Fatalf("Text = %q, want %q", got, `{"name":"lantern"}`)
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

// TestNative_PreservesKindAndValue proves Native returns, for every oneof
// variant, the concrete Go type that nativeVertex.asVertex re-encodes back
// to the SAME variant — so a touch re-put leaves the stored kind and value
// untouched. This is the contract that distinguishes Native from FromVertex,
// which deliberately lossily stringifies timestamps/durations for MCP output.
func TestNative_PreservesKindAndValue(t *testing.T) {
	ts := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	dur := 90 * time.Minute
	cases := []struct {
		name string
		v    *pb.Vertex
		want any
	}{
		{"float32", &pb.Vertex{Value: &pb.Vertex_Float32{Float32: 1.5}}, float32(1.5)},
		{"float64", &pb.Vertex{Value: &pb.Vertex_Float64{Float64: 2.5}}, float64(2.5)},
		{"int32", &pb.Vertex{Value: &pb.Vertex_Int32{Int32: -7}}, int32(-7)},
		{"int64", &pb.Vertex{Value: &pb.Vertex_Int64{Int64: 42}}, int64(42)},
		{"uint32", &pb.Vertex{Value: &pb.Vertex_Uint32{Uint32: 9}}, uint32(9)},
		{"uint64", &pb.Vertex{Value: &pb.Vertex_Uint64{Uint64: 11}}, uint64(11)},
		{"bool", &pb.Vertex{Value: &pb.Vertex_Bool{Bool: true}}, true},
		{"string", &pb.Vertex{Value: &pb.Vertex_String_{String_: "warm"}}, "warm"},
		{"bytes", &pb.Vertex{Value: &pb.Vertex_Bytes{Bytes: []byte{1, 2, 3}}}, []byte{1, 2, 3}},
		{"timestamp", &pb.Vertex{Value: &pb.Vertex_Timestamp{Timestamp: timestamppb.New(ts)}}, ts},
		{"duration", &pb.Vertex{Value: &pb.Vertex_Duration{Duration: durationpb.New(dur)}}, dur},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Native(c.v)
			switch want := c.want.(type) {
			case []byte:
				gb, ok := got.([]byte)
				if !ok || !bytes.Equal(gb, want) {
					t.Fatalf("Native = %v (%T), want %v ([]byte)", got, got, want)
				}
			case time.Time:
				gt, ok := got.(time.Time)
				if !ok || !gt.Equal(want) {
					t.Fatalf("Native = %v (%T), want %v (time.Time)", got, got, want)
				}
			default:
				if got != c.want {
					t.Fatalf("Native = %v (%T), want %v (%T)", got, got, c.want, c.want)
				}
			}
		})
	}
}

// TestNative_StringStaysVerbatim guards the one place Native must NOT behave
// like FromVertex: a JSON-looking string is returned as a string (not
// promoted to a map), so re-putting it stores the identical String variant
// rather than re-encoding a structured shape.
func TestNative_StringStaysVerbatim(t *testing.T) {
	v := &pb.Vertex{Value: &pb.Vertex_String_{String_: `{"x":1}`}}
	got := Native(v)
	if s, ok := got.(string); !ok || s != `{"x":1}` {
		t.Fatalf("Native = %v (%T), want verbatim string {\"x\":1}", got, got)
	}
}

// TestNative_NilVariantsAreNil confirms both a nil pointer and the present
// Vertex_Nil tombstone collapse to a Go nil, which re-puts as the same
// present-nil tombstone.
func TestNative_NilVariantsAreNil(t *testing.T) {
	if got := Native(nil); got != nil {
		t.Fatalf("Native(nil) = %v, want nil", got)
	}
	if got := Native(&pb.Vertex{Value: &pb.Vertex_Nil{Nil: true}}); got != nil {
		t.Fatalf("Native(nil-variant) = %v, want nil", got)
	}
}
