package client

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
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
