package securitytransport

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestEdgePolicyCodec(t *testing.T) {
	for mode := pb.EdgeEndpointMode_EDGE_ENDPOINT_MODE_READ_READ; mode <= pb.EdgeEndpointMode_EDGE_ENDPOINT_MODE_WRITE_READ; mode++ {
		wire := &pb.SecurityEdgePolicy{Version: 1, Mode: mode}
		policy, err := decodeEdgePolicy(wire)
		if err != nil || !proto.Equal(wire, encodeEdgePolicy(*policy)) {
			t.Fatal(mode, err)
		}
		change, err := decodeSecurityChange(&pb.SecurityChange{Operation: &pb.SecurityChange_PutEdgePolicy{PutEdgePolicy: wire}})
		if err != nil || change.Kind != security.PutEdgePolicy || change.EdgePolicy.Mode != policy.Mode {
			t.Fatal("change", err)
		}
	}
	for _, wire := range []*pb.SecurityEdgePolicy{nil, {Version: 1}, {Version: 2, Mode: 1}, {Version: 1, Mode: 99}} {
		if _, err := decodeEdgePolicy(wire); err == nil {
			t.Fatal("invalid mode accepted")
		}
	}
}
