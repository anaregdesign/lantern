package client

import (
	pb "github.com/anaregdesign/lantern-proto/go/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"reflect"
	"testing"
	"time"
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.BoolValue()
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.BytesValue()
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.FloatValue()
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.IntValue()
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.IsNil(); got != tt.want {
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.StringValue()
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.TimeValue()
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.UIntValue()
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
		value      interface{}
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
	for _, tt := range tests {
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
