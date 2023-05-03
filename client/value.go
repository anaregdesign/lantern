package client

import (
	"errors"
	pb "github.com/anaregdesign/lantern-proto/go/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

var ErrInvalidType = errors.New("invalid type")

type nativeVertex struct {
	key        string
	value      interface{}
	expiration time.Time
}

func (v nativeVertex) asVertex() (*pb.Vertex, error) {
	switch x := v.value.(type) {
	case int:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Int64{
				Int64: int64(x),
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case int32:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Int32{
				Int32: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case int64:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Int64{
				Int64: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case float32:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Float32{
				Float32: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case float64:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Float64{
				Float64: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case uint32:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Uint32{
				Uint32: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case string:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_String_{
				String_: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case bool:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Bool{
				Bool: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case []byte:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Bytes{
				Bytes: x,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case time.Time:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Timestamp{
				Timestamp: timestamppb.New(x),
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	case nil:
		return &pb.Vertex{
			Key: v.key,
			Value: &pb.Vertex_Nil{
				Nil: true,
			},
			Expiration: timestamppb.New(v.expiration),
		}, nil

	default:
		return nil, ErrInvalidType
	}
}

type Vertex pb.Vertex

func (v *Vertex) IntValue() (int, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Int64:
		return int(x.Int64), nil

	case *pb.Vertex_Int32:
		return int(x.Int32), nil

	default:
		return 0, ErrInvalidType
	}
}

func (v *Vertex) UIntValue() (uint, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Uint32:
		return uint(x.Uint32), nil

	case *pb.Vertex_Uint64:
		return uint(x.Uint64), nil

	default:
		return 0, ErrInvalidType
	}
}

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
	switch x := v.Value.(type) {
	case *pb.Vertex_String_:
		return x.String_, nil

	default:
		return "", ErrInvalidType
	}
}

func (v *Vertex) BoolValue() (bool, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Bool:
		return x.Bool, nil

	default:
		return false, ErrInvalidType
	}
}

func (v *Vertex) BytesValue() ([]byte, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Bytes:
		return x.Bytes, nil

	default:
		return nil, ErrInvalidType
	}
}

func (v *Vertex) TimeValue() (time.Time, error) {
	switch x := v.Value.(type) {
	case *pb.Vertex_Timestamp:
		return x.Timestamp.AsTime(), nil

	default:
		return time.Time{}, ErrInvalidType
	}
}

func (v *Vertex) IsNil() bool {
	switch x := v.Value.(type) {
	case *pb.Vertex_Nil:
		return x.Nil

	default:
		return false
	}
}
