package client

import (
	"errors"
	"math"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/sdks/go/gen/graph/v1"
)

func TestNativeVertex_AsVertex_IntegerWidths(t *testing.T) {
	exp := time.Now().Add(time.Minute)
	cases := []struct {
		name string
		in   any
		kind VertexKind
	}{
		{"int", int(7), VertexKindInt64},
		{"int8", int8(7), VertexKindInt32},
		{"int16", int16(7), VertexKindInt32},
		{"int32", int32(7), VertexKindInt32},
		{"int64", int64(7), VertexKindInt64},
		{"uint", uint(7), VertexKindUint64},
		{"uint8", uint8(7), VertexKindUint32},
		{"uint16", uint16(7), VertexKindUint32},
		{"uint32", uint32(7), VertexKindUint32},
		{"uint64", uint64(7), VertexKindUint64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := nativeVertex{key: "k", value: tc.in, expiration: exp}.asVertex()
			if err != nil {
				t.Fatalf("asVertex: %v", err)
			}
			wrapped := (*Vertex)(v)
			if got := wrapped.Kind(); got != tc.kind {
				t.Errorf("Kind = %v, want %v", got, tc.kind)
			}
		})
	}
}

func TestVertex_IntValue_Overflow(t *testing.T) {
	v := &Vertex{Value: &pb.Vertex_Uint64{Uint64: math.MaxUint64}}
	if _, err := v.IntValue(); !errors.Is(err, ErrOverflow) {
		t.Errorf("err = %v, want ErrOverflow", err)
	}
}

func TestVertex_UIntValue_Overflow(t *testing.T) {
	v := &Vertex{Value: &pb.Vertex_Int64{Int64: -1}}
	if _, err := v.UIntValue(); !errors.Is(err, ErrOverflow) {
		t.Errorf("err = %v, want ErrOverflow", err)
	}
}

func TestVertex_IntValue_CrossWidth(t *testing.T) {
	v := &Vertex{Value: &pb.Vertex_Uint32{Uint32: 42}}
	got, err := v.IntValue()
	if err != nil {
		t.Fatalf("IntValue: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestVertex_Kind(t *testing.T) {
	cases := []struct {
		name string
		v    *Vertex
		want VertexKind
	}{
		{"nil-vertex", nil, VertexKindUnset},
		{"unset", &Vertex{}, VertexKindUnset},
		{"string", &Vertex{Value: &pb.Vertex_String_{String_: "x"}}, VertexKindString},
		{"bool", &Vertex{Value: &pb.Vertex_Bool{Bool: true}}, VertexKindBool},
		{"bytes", &Vertex{Value: &pb.Vertex_Bytes{Bytes: []byte{1}}}, VertexKindBytes},
		{"nil-value", &Vertex{Value: &pb.Vertex_Nil{Nil: true}}, VertexKindNil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.Kind(); got != tc.want {
				t.Errorf("Kind = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVertex_ExpirationTime(t *testing.T) {
	if got := (*Vertex)(nil).ExpirationTime(); !got.IsZero() {
		t.Errorf("nil vertex expiration = %v, want zero", got)
	}
	if got := (&Vertex{}).ExpirationTime(); !got.IsZero() {
		t.Errorf("empty expiration = %v, want zero", got)
	}
}
