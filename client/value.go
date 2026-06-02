package client

import (
	"encoding/json"
	"errors"
	"math"
	"time"

	pb "github.com/anaregdesign/lantern/gen/go/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrInvalidType is returned by a Vertex.*Value method when the underlying
// oneof variant does not match the requested Go type and no safe coercion is
// available.
var ErrInvalidType = errors.New("invalid type")

// ErrOverflow is returned when a value is read into a Go type that cannot
// represent it (e.g. reading a negative int64 as uint, or a uint64 above
// math.MaxInt64 as int).
var ErrOverflow = errors.New("value overflow")

// nativeVertex bridges a Go-native value into a protobuf Vertex. The type
// switch in asVertex is the authoritative mapping from Go types to oneof
// variants — when adding a new type, update both this switch and the matching
// Vertex.*Value reader so round-trips stay consistent.
type nativeVertex struct {
	key        string
	value      any
	expiration time.Time
}

func (v nativeVertex) asVertex() (*pb.Vertex, error) {
	exp := timestamppb.New(v.expiration)
	switch x := v.value.(type) {
	case int:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Int64{Int64: int64(x)}}, nil
	case int8:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Int32{Int32: int32(x)}}, nil
	case int16:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Int32{Int32: int32(x)}}, nil
	case int32:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Int32{Int32: x}}, nil
	case int64:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Int64{Int64: x}}, nil

	case uint:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Uint64{Uint64: uint64(x)}}, nil
	case uint8:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Uint32{Uint32: uint32(x)}}, nil
	case uint16:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Uint32{Uint32: uint32(x)}}, nil
	case uint32:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Uint32{Uint32: x}}, nil
	case uint64:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Uint64{Uint64: x}}, nil

	case float32:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Float32{Float32: x}}, nil
	case float64:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Float64{Float64: x}}, nil

	case string:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_String_{String_: x}}, nil
	case bool:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Bool{Bool: x}}, nil
	case []byte:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Bytes{Bytes: x}}, nil
	case time.Time:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Timestamp{Timestamp: timestamppb.New(x)}}, nil
	case nil:
		return &pb.Vertex{Key: v.key, Expiration: exp, Value: &pb.Vertex_Nil{Nil: true}}, nil

	default:
		return nil, ErrInvalidType
	}
}

// Vertex is a thin type alias over the generated protobuf Vertex that adds
// Go-friendly value accessors. Convert a *pb.Vertex with (*Vertex)(p).
type Vertex pb.Vertex

// VertexKind identifies which oneof variant is set on a Vertex. Use
// Vertex.Kind to dispatch without writing a switch over Value.(type).
type VertexKind int

const (
	VertexKindUnset VertexKind = iota
	VertexKindFloat32
	VertexKindFloat64
	VertexKindInt32
	VertexKindInt64
	VertexKindUint32
	VertexKindUint64
	VertexKindBool
	VertexKindString
	VertexKindBytes
	VertexKindTimestamp
	VertexKindNil
)

// Kind reports which oneof variant is set on v.
func (v *Vertex) Kind() VertexKind {
	if v == nil {
		return VertexKindUnset
	}
	switch v.Value.(type) {
	case *pb.Vertex_Float32:
		return VertexKindFloat32
	case *pb.Vertex_Float64:
		return VertexKindFloat64
	case *pb.Vertex_Int32:
		return VertexKindInt32
	case *pb.Vertex_Int64:
		return VertexKindInt64
	case *pb.Vertex_Uint32:
		return VertexKindUint32
	case *pb.Vertex_Uint64:
		return VertexKindUint64
	case *pb.Vertex_Bool:
		return VertexKindBool
	case *pb.Vertex_String_:
		return VertexKindString
	case *pb.Vertex_Bytes:
		return VertexKindBytes
	case *pb.Vertex_Timestamp:
		return VertexKindTimestamp
	case *pb.Vertex_Nil:
		return VertexKindNil
	default:
		return VertexKindUnset
	}
}

// ExpirationTime returns the absolute expiration carried by v, or the zero
// time if no expiration was set on the server response.
func (v *Vertex) ExpirationTime() time.Time {
	if v == nil || v.Expiration == nil {
		return time.Time{}
	}
	return v.Expiration.AsTime()
}

// IntValue returns the underlying signed integer value. It also accepts
// unsigned variants and reports ErrOverflow when a Uint64 value exceeds
// math.MaxInt64.
func (v *Vertex) IntValue() (int, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Int32:
		return int(x.Int32), nil
	case *pb.Vertex_Int64:
		return int(x.Int64), nil
	case *pb.Vertex_Uint32:
		return int(x.Uint32), nil
	case *pb.Vertex_Uint64:
		if x.Uint64 > math.MaxInt64 {
			return 0, ErrOverflow
		}
		return int(x.Uint64), nil
	default:
		return 0, ErrInvalidType
	}
}

// UIntValue returns the underlying unsigned integer value. It also accepts
// signed variants and reports ErrOverflow when the value is negative.
func (v *Vertex) UIntValue() (uint, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Uint32:
		return uint(x.Uint32), nil
	case *pb.Vertex_Uint64:
		return uint(x.Uint64), nil
	case *pb.Vertex_Int32:
		if x.Int32 < 0 {
			return 0, ErrOverflow
		}
		return uint(x.Int32), nil
	case *pb.Vertex_Int64:
		if x.Int64 < 0 {
			return 0, ErrOverflow
		}
		return uint(x.Int64), nil
	default:
		return 0, ErrInvalidType
	}
}

// FloatValue widens any numeric oneof variant to float64.
func (v *Vertex) FloatValue() (float64, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Int64:
		return float64(x.Int64), nil
	case *pb.Vertex_Int32:
		return float64(x.Int32), nil
	case *pb.Vertex_Uint32:
		return float64(x.Uint32), nil
	case *pb.Vertex_Uint64:
		return float64(x.Uint64), nil
	case *pb.Vertex_Float32:
		return float64(x.Float32), nil
	case *pb.Vertex_Float64:
		return x.Float64, nil
	default:
		return 0, ErrInvalidType
	}
}

func (v *Vertex) StringValue() (string, error) {
	if x, ok := v.Value.(*pb.Vertex_String_); ok {
		return x.String_, nil
	}
	return "", ErrInvalidType
}

func (v *Vertex) BoolValue() (bool, error) {
	if x, ok := v.Value.(*pb.Vertex_Bool); ok {
		return x.Bool, nil
	}
	return false, ErrInvalidType
}

func (v *Vertex) BytesValue() ([]byte, error) {
	if x, ok := v.Value.(*pb.Vertex_Bytes); ok {
		return x.Bytes, nil
	}
	return nil, ErrInvalidType
}

func (v *Vertex) TimeValue() (time.Time, error) {
	if x, ok := v.Value.(*pb.Vertex_Timestamp); ok {
		return x.Timestamp.AsTime(), nil
	}
	return time.Time{}, ErrInvalidType
}

func (v *Vertex) IsNil() bool {
	if x, ok := v.Value.(*pb.Vertex_Nil); ok {
		return x.Nil
	}
	return false
}

// MarshalJSON renders a Vertex as a stable, human-readable JSON object so
// callers don't have to peek at protobuf-generated oneof field names (e.g.
// "String_", "Int64", "Nil"). The shape is:
//
//	{
//	  "key":        "<key>",                // omitted when empty
//	  "type":       "string"|"int32"|...,   // VertexKind in lowercase
//	  "value":      <typed JSON value>,     // null for nil/unset
//	  "expiration": "<RFC3339Nano>"         // omitted when zero
//	}
//
// Bytes are base64-encoded (Go's default for []byte); timestamps are RFC3339Nano.
func (v *Vertex) MarshalJSON() ([]byte, error) {
	out := struct {
		Key        string `json:"key,omitempty"`
		Type       string `json:"type"`
		Value      any    `json:"value"`
		Expiration string `json:"expiration,omitempty"`
	}{}
	if v == nil {
		out.Type = "unset"
		return json.Marshal(out)
	}
	out.Key = v.Key
	if t := v.ExpirationTime(); !t.IsZero() {
		out.Expiration = t.Format(time.RFC3339Nano)
	}
	switch x := v.Value.(type) {
	case *pb.Vertex_Float32:
		out.Type, out.Value = "float32", x.Float32
	case *pb.Vertex_Float64:
		out.Type, out.Value = "float64", x.Float64
	case *pb.Vertex_Int32:
		out.Type, out.Value = "int32", x.Int32
	case *pb.Vertex_Int64:
		out.Type, out.Value = "int64", x.Int64
	case *pb.Vertex_Uint32:
		out.Type, out.Value = "uint32", x.Uint32
	case *pb.Vertex_Uint64:
		out.Type, out.Value = "uint64", x.Uint64
	case *pb.Vertex_Bool:
		out.Type, out.Value = "bool", x.Bool
	case *pb.Vertex_String_:
		out.Type, out.Value = "string", x.String_
	case *pb.Vertex_Bytes:
		out.Type, out.Value = "bytes", x.Bytes
	case *pb.Vertex_Timestamp:
		out.Type, out.Value = "timestamp", x.Timestamp.AsTime().Format(time.RFC3339Nano)
	case *pb.Vertex_Nil:
		out.Type, out.Value = "nil", nil
	default:
		out.Type, out.Value = "unset", nil
	}
	return json.Marshal(out)
}
