package service

import (
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type testReplicationProjection struct {
	graph       *pb.Mutation
	replication *pb.Mutation
	err         error
}

func (p *testReplicationProjection) GraphMutation() *pb.Mutation {
	return p.graph
}

func (p *testReplicationProjection) ReplicationMutation() (*pb.Mutation, error) {
	return p.replication, p.err
}

func TestReplicationFrameUsesCanonicalSubscribeProjection(t *testing.T) {
	small := &pb.Mutation{
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{
			DeleteVertex: &pb.DeleteVertexRequest{Key: "small"},
		}},
	}
	large := &pb.Mutation{
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{
				Key:   "large",
				Value: &pb.Vertex_Bytes{Bytes: make([]byte, 1024)},
			}},
		}},
	}

	frame, err := subscribeMutationFrame(small)
	if err != nil {
		t.Fatal(err)
	}
	innerSize := proto.Size(small)
	frameSize := proto.Size(frame)
	if frameSize <= innerSize {
		t.Fatalf("SubscribeResponse size = %d, Mutation size = %d; want outer framing overhead", frameSize, innerSize)
	}
	if _, err := validateReplicationFrameSize(small, innerSize); err == nil {
		t.Fatal("inner-size limit accepted the larger SubscribeResponse")
	}

	projected := &testReplicationProjection{graph: small, replication: large}
	frame, err = subscribeMutationFrame(projected)
	if err != nil {
		t.Fatal(err)
	}
	if frame.GetMutation() != large {
		t.Fatal("receipt-capable payload used graph-only projection")
	}
	if _, err := validateReplicationFrameSize(projected, proto.Size(small)+16); err == nil {
		t.Fatal("receipt projection bypassed frame-size admission")
	}

	projected.err = errors.New("broken receipt projection")
	if _, err := subscribeMutationFrame(projected); err == nil ||
		!strings.Contains(err.Error(), "broken receipt projection") {
		t.Fatalf("projection error = %v", err)
	}
}

func TestLanternServiceReplicationFrameRejectsWithMetric(t *testing.T) {
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](0))
	svc.replicationFrameCertified = true
	svc.replicationSendMaxBytes = 64
	var reasons []string
	svc.WithValidationRejectHook(func(reason string) {
		reasons = append(reasons, reason)
	})
	mutation := &pb.Mutation{
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{
				Key:   "large",
				Value: &pb.Vertex_Bytes{Bytes: make([]byte, 256)},
			}},
		}},
	}

	err := svc.validateReplicationFrame(mutation)
	if connect.CodeOf(err) != connect.CodeResourceExhausted ||
		!strings.Contains(err.Error(), "LANTERN_MAX_SEND_MSG_BYTES=64") {
		t.Fatalf("oversized frame error = %v, want explicit ResourceExhausted", err)
	}
	if len(reasons) != 1 || reasons[0] != "replication_frame" {
		t.Fatalf("validation reject reasons = %v", reasons)
	}
}

func TestLanternServiceReceiptWireCapacityRejectsWithUnlimitedSendCap(t *testing.T) {
	for _, capacity := range []error{
		errReceiptEdgeDeleteWireCapacity,
		errReceiptVertexDeleteWireCapacity,
		errReceiptVertexPutWireCapacity,
	} {
		t.Run(capacity.Error(), func(t *testing.T) {
			svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](0))
			svc.replicationFrameCertified = true
			var reasons []string
			svc.WithValidationRejectHook(func(reason string) {
				reasons = append(reasons, reason)
			})
			err := svc.validateReplicationFrame(&testReplicationProjection{err: capacity})
			if connect.CodeOf(err) != connect.CodeResourceExhausted || !errors.Is(err, capacity) {
				t.Fatalf("intrinsic wire capacity = %v, want ResourceExhausted wrapping %v", err, capacity)
			}
			if len(reasons) != 1 || reasons[0] != "replication_frame" {
				t.Fatalf("intrinsic wire capacity reject reasons = %v", reasons)
			}
		})
	}
}

func TestGraphPublicationFrameUsesLoggableEffectProjection(t *testing.T) {
	origin := [16]byte{1}
	mutation := &pb.Mutation{
		Origin: origin[:],
		Seq:    1,
		Hlc:    &pb.HLCTimestamp{WallNs: 1, NodeId: origin[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
			DeleteVertices: &pb.DeleteVerticesRequest{
				Keys: []string{"one", "two", "three", "four"},
			},
		}},
	}
	rawFrame, err := subscribeMutationFrame(mutation)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := maximalGraphEffectPublication(mutation)
	if err != nil {
		t.Fatal(err)
	}
	effectFrame, err := subscribeMutationFrame(effect)
	if err != nil {
		t.Fatal(err)
	}
	rawSize := proto.Size(rawFrame)
	effectSize := proto.Size(effectFrame)
	if effectSize != rawSize || !proto.Equal(effectFrame, rawFrame) {
		t.Fatalf("loggable effect frame differs from raw mutation: effect=%d raw=%d", effectSize, rawSize)
	}

	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](0))
	svc.replicationFrameCertified = true
	svc.replicationSendMaxBytes = rawSize - 1
	if err := svc.validateGraphPublicationShape(mutation); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("maximal publication effect validation = %v, want ResourceExhausted", err)
	}
}
