package client

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestVertex_BoolValue(t *testing.T) {
	tests := []struct {
		name    string
		v       Vertex
		want    bool
		wantErr bool
	}{
		{
			name: "BoolValue",
			v: Vertex{
				Value: &pb.Vertex_Bool{
					Bool: true,
				},
			},
			want:    true,
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := BoolValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("BoolValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("BoolValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_BytesValue(t *testing.T) {
	tests := []struct {
		name    string
		v       Vertex
		want    []byte
		wantErr bool
	}{
		{
			name: "BytesValue",
			v: Vertex{
				Value: &pb.Vertex_Bytes{
					Bytes: []byte("test"),
				},
			},
			want:    []byte("test"),
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := BytesValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("BytesValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BytesValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_FloatValue(t *testing.T) {
	tests := []struct {
		name    string
		v       Vertex
		want    float64
		wantErr bool
	}{
		{
			name: "FloatValue",
			v: Vertex{
				Value: &pb.Vertex_Float64{
					Float64: 1.1,
				},
			},
			want:    1.1,
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := FloatValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("FloatValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FloatValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_IntValue(t *testing.T) {
	tests := []struct {
		name    string
		v       Vertex
		want    int
		wantErr bool
	}{
		{
			name: "IntValue",
			v: Vertex{
				Value: &pb.Vertex_Int64{
					Int64: 1,
				},
			},
			want:    1,
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := IntValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("IntValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IntValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_IsNil(t *testing.T) {
	tests := []struct {
		name string
		v    Vertex
		want bool
	}{
		{
			name: "IsNil",
			v: Vertex{
				Value: &pb.Vertex_Nil{
					Nil: true,
				},
			},
			want: true,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNil(&tt.v); got != tt.want {
				t.Errorf("IsNil() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_StringValue(t *testing.T) {
	tests := []struct {
		name    string
		v       Vertex
		want    string
		wantErr bool
	}{
		{
			name: "StringValue",
			v: Vertex{
				Value: &pb.Vertex_String_{
					String_: "test",
				},
			},
			want:    "test",
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := StringValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("StringValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("StringValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_TimeValue(t *testing.T) {
	now := timestamppb.Now()
	tests := []struct {
		name    string
		v       Vertex
		want    time.Time
		wantErr bool
	}{
		{
			name: "TimeValue",
			v: Vertex{
				Value: &pb.Vertex_Timestamp{
					Timestamp: now,
				},
			},
			want:    now.AsTime(),
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := TimeValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("TimeValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TimeValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_DurationValue(t *testing.T) {
	d := 90 * time.Minute
	tests := []struct {
		name    string
		v       Vertex
		want    time.Duration
		wantErr bool
	}{
		{
			name: "DurationValue",
			v: Vertex{
				Value: &pb.Vertex_Duration{
					Duration: durationpb.New(d),
				},
			},
			want:    d,
			wantErr: false,
		},
		{
			name:    "wrong type",
			v:       Vertex{Value: &pb.Vertex_Int64{Int64: 1}},
			want:    0,
			wantErr: true,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := DurationValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("DurationValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DurationValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertex_UIntValue(t *testing.T) {
	tests := []struct {
		name    string
		v       Vertex
		want    uint
		wantErr bool
	}{
		{
			name: "UIntValue",
			v: Vertex{
				Value: &pb.Vertex_Uint64{
					Uint64: 1,
				},
			},
			want:    1,
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := UIntValue(&tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("UIntValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("UIntValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_nativeVertex_asVertex(t *testing.T) {
	now := timestamppb.Now()
	type fields struct {
		key        string
		value      any
		expiration time.Time
	}
	tests := []struct {
		name    string
		fields  fields
		want    *pb.Vertex
		wantErr bool
	}{
		{
			name: "IntValue",
			fields: fields{
				key:        "test",
				value:      1,
				expiration: now.AsTime(),
			},
			want: &pb.Vertex{
				Key: "test",
				Value: &pb.Vertex_Int64{
					Int64: 1,
				},
				Expiration: now,
			},
			wantErr: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			v := nativeVertex{
				key:        tt.fields.key,
				value:      tt.fields.value,
				expiration: tt.fields.expiration,
			}
			got, err := v.asVertex()
			if (err != nil) != tt.wantErr {
				t.Errorf("asVertex() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("asVertex() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMarshalVertexJSON(t *testing.T) {
	exp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		v    *Vertex
		want string
	}{
		{
			name: "string with key and expiration",
			v: &Vertex{
				Key:        "a",
				Expiration: timestamppb.New(exp),
				Value:      &pb.Vertex_String_{String_: "A"},
			},
			want: `{"key":"a","type":"string","value":"A","expiration":"2026-01-02T03:04:05Z"}`,
		},
		{
			name: "int64 without expiration",
			v:    &Vertex{Value: &pb.Vertex_Int64{Int64: 42}},
			want: `{"type":"int64","value":42}`,
		},
		{
			name: "bool true",
			v:    &Vertex{Value: &pb.Vertex_Bool{Bool: true}},
			want: `{"type":"bool","value":true}`,
		},
		{
			name: "bytes base64",
			v:    &Vertex{Value: &pb.Vertex_Bytes{Bytes: []byte("hi")}},
			want: `{"type":"bytes","value":"aGk="}`,
		},
		{
			name: "timestamp RFC3339Nano",
			v:    &Vertex{Value: &pb.Vertex_Timestamp{Timestamp: timestamppb.New(ts)}},
			want: `{"type":"timestamp","value":"2026-06-01T00:00:00Z"}`,
		},
		{
			name: "nil sentinel",
			v:    &Vertex{Value: &pb.Vertex_Nil{Nil: true}},
			want: `{"type":"nil","value":null}`,
		},
		{
			name: "duration",
			v:    &Vertex{Value: &pb.Vertex_Duration{Duration: durationpb.New(90 * time.Minute)}},
			want: `{"type":"duration","value":"1h30m0s"}`,
		},
		{
			name: "nil vertex",
			v:    nil,
			want: `{"type":"unset","value":null}`,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, err := MarshalVertexJSON(tt.v)
			if err != nil {
				t.Fatalf("MarshalVertexJSON() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("MarshalVertexJSON() got = %s, want %s", got, tt.want)
			}
		})
	}
}

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
			if got := Kind(v); got != tc.kind {
				t.Errorf("Kind = %v, want %v", got, tc.kind)
			}
		})
	}
}

func TestVertex_IntValue_Overflow(t *testing.T) {
	v := &Vertex{Value: &pb.Vertex_Uint64{Uint64: math.MaxUint64}}
	if _, err := IntValue(v); !errors.Is(err, ErrOverflow) {
		t.Errorf("err = %v, want ErrOverflow", err)
	}
}

func TestVertex_UIntValue_Overflow(t *testing.T) {
	v := &Vertex{Value: &pb.Vertex_Int64{Int64: -1}}
	if _, err := UIntValue(v); !errors.Is(err, ErrOverflow) {
		t.Errorf("err = %v, want ErrOverflow", err)
	}
}

func TestVertex_IntValue_CrossWidth(t *testing.T) {
	v := &Vertex{Value: &pb.Vertex_Uint32{Uint32: 42}}
	got, err := IntValue(v)
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
			if got := Kind(tc.v); got != tc.want {
				t.Errorf("Kind = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVertexExpiration(t *testing.T) {
	if got := VertexExpiration(nil); !got.IsZero() {
		t.Errorf("nil vertex expiration = %v, want zero", got)
	}
	if got := VertexExpiration(&Vertex{}); !got.IsZero() {
		t.Errorf("empty expiration = %v, want zero", got)
	}
}

// TestVertexJSON_RoundTrip exercises MarshalVertexJSON ↔ UnmarshalVertexJSON
// across every value type, including int64/uint64 magnitudes above 2^53
// (which would corrupt through a float64 intermediate) and a value-bearing
// expiration.
func TestVertexJSON_RoundTrip(t *testing.T) {
	exp := time.Date(2026, 6, 18, 12, 30, 45, 123456789, time.UTC)
	cases := []*Vertex{
		{Key: "f32", Value: &pb.Vertex_Float32{Float32: 1.5}},
		{Key: "f64", Value: &pb.Vertex_Float64{Float64: 3.141592653589793}},
		{Key: "i32", Value: &pb.Vertex_Int32{Int32: -42}},
		{Key: "i64max", Value: &pb.Vertex_Int64{Int64: math.MaxInt64}},
		{Key: "i64min", Value: &pb.Vertex_Int64{Int64: math.MinInt64}},
		{Key: "u32", Value: &pb.Vertex_Uint32{Uint32: 42}},
		{Key: "u64max", Value: &pb.Vertex_Uint64{Uint64: math.MaxUint64}},
		{Key: "bool", Value: &pb.Vertex_Bool{Bool: true}},
		{Key: "str", Value: &pb.Vertex_String_{String_: "héllo \"world\"\n"}},
		{Key: "bytes", Value: &pb.Vertex_Bytes{Bytes: []byte{0x00, 0x01, 0xff, 0x7f}}},
		{Key: "ts", Value: &pb.Vertex_Timestamp{Timestamp: timestamppb.New(exp)}},
		{Key: "dur", Value: &pb.Vertex_Duration{Duration: durationpb.New(90*time.Minute + 500*time.Millisecond)}},
		{Key: "nilval", Value: &pb.Vertex_Nil{Nil: true}},
		{Key: "noval"},
		{Key: "withexp", Expiration: timestamppb.New(exp), Value: &pb.Vertex_String_{String_: "x"}},
	}
	for _, want := range cases {
		b, err := MarshalVertexJSON(want)
		if err != nil {
			t.Fatalf("%s: marshal: %v", want.Key, err)
		}
		got, err := UnmarshalVertexJSON(b)
		if err != nil {
			t.Fatalf("%s: unmarshal: %v (json=%s)", want.Key, err, b)
		}
		if !proto.Equal(got, want) {
			t.Errorf("%s round-trip mismatch:\n got=%v\nwant=%v\njson=%s", want.Key, got, want, b)
		}
	}
}

func TestEdgeJSON_RoundTrip(t *testing.T) {
	exp := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	cases := []*Edge{
		{Tail: "a", Head: "b", Weight: 1.5},
		{Tail: "a", Head: "b", Weight: -2.25, Expiration: timestamppb.New(exp)},
		{Tail: "x:1", Head: "x:2", Weight: 0},
	}
	for i, want := range cases {
		b, err := MarshalEdgeJSON(want)
		if err != nil {
			t.Fatalf("[%d] marshal: %v", i, err)
		}
		got, err := UnmarshalEdgeJSON(b)
		if err != nil {
			t.Fatalf("[%d] unmarshal: %v", i, err)
		}
		if !proto.Equal(got, want) {
			t.Errorf("[%d] round-trip mismatch: got=%v want=%v json=%s", i, got, want, b)
		}
	}
}

// FuzzUnmarshalVertexJSON fuzzes the JSON→Vertex decode path used by the NDJSON
// backup/restore codec. A clean decode must (a) survive every value accessor
// without panicking, and (b) re-marshal via MarshalVertexJSON to bytes that
// themselves decode again — the marshal/unmarshal pair must stay stable.
// Inputs MarshalVertexJSON legitimately cannot re-encode are tolerated (it must
// fail cleanly, not panic), keeping the fuzz focused on real defects.
func FuzzUnmarshalVertexJSON(f *testing.F) {
	seeds := []string{
		`{"key":"a","type":"int64","value":42}`,
		`{"key":"u","type":"uint64","value":18446744073709551615}`,
		`{"key":"f","type":"float64","value":3.14}`,
		`{"key":"s","type":"string","value":"hi"}`,
		`{"key":"b","type":"bool","value":true}`,
		`{"key":"by","type":"bytes","value":"aGVsbG8="}`,
		`{"key":"t","type":"timestamp","value":"2020-01-01T00:00:00Z"}`,
		`{"key":"d","type":"duration","value":"1h30m"}`,
		`{"key":"n","type":"nil","value":null}`,
		`{"key":"x","type":"int64","value":42,"expiration":"2030-01-01T00:00:00Z"}`,
		`{`,
		``,
		`{"type":"bogus"}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data string) {
		v, err := UnmarshalVertexJSON([]byte(data))
		if err != nil {
			return
		}
		// No accessor may panic on a successfully decoded vertex.
		_ = Kind(v)
		_, _ = IntValue(v)
		_, _ = UIntValue(v)
		_, _ = FloatValue(v)
		_, _ = StringValue(v)
		_, _ = BoolValue(v)
		_, _ = BytesValue(v)
		_, _ = TimeValue(v)
		_, _ = DurationValue(v)
		_ = IsNil(v)
		_ = VertexExpiration(v)
		// The marshal/unmarshal pair must stay stable: bytes produced by
		// MarshalVertexJSON must decode again. Values it cannot re-encode are
		// out of scope (it must error cleanly rather than panic).
		b, err := MarshalVertexJSON(v)
		if err != nil {
			return
		}
		if _, err := UnmarshalVertexJSON(b); err != nil {
			t.Fatalf("re-decode of MarshalVertexJSON output: %v", err)
		}
	})
}
