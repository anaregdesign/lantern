package rpcwire

import (
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestBoundedErrorDetail(t *testing.T) {
	for _, size := range []int{4096, 4097} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			message := wrapperspb.Bytes(make([]byte, size-3))
			if proto.Size(message) != size {
				t.Fatal("fixture size drift")
			}
			detail, err := BoundedErrorDetail(message)
			if size > 4096 {
				if err == nil || detail != nil {
					t.Fatal("oversized detail accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := detail.Value()
			if err != nil || !proto.Equal(decoded, message) {
				t.Fatal("detail changed", err)
			}
		})
	}
	if detail, err := BoundedErrorDetail(nil); err == nil || detail != nil {
		t.Fatal("nil detail accepted")
	}
}
