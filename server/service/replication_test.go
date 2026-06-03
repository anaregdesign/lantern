package service

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// TestLanternService_MutationLog_BurstAppendsMonotone exercises the wire-in
// from issue #179: a burst of N plural-write RPCs must produce N entries on
// the in-memory mutation log with strictly monotonic Seq starting at 1, and
// the onAppend metric callback must fire exactly once per append.
func TestLanternService_MutationLog_BurstAppendsMonotone(t *testing.T) {
	const N = 100

	log := mutationlog.New(mutationlog.Options{Capacity: 2 * N, SubscriberBuffer: 2 * N})
	t.Cleanup(func() { _ = log.Close() })

	clock := hlc.New(hlc.NodeID{0x11, 0x22, 0x33, 0x44}, hlc.Options{})

	var appendCount int
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithReplication(log, clock, func() { appendCount++ })

	ch, cancel, err := log.Subscribe(0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = cancel() })

	for i := 0; i < N; i++ {
		_, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{{Key: keyFor(i)}},
		})
		if err != nil {
			t.Fatalf("PutVertices[%d] error: %v", i, err)
		}
	}

	if appendCount != N {
		t.Fatalf("onAppend count = %d, want %d", appendCount, N)
	}
	first, ok1 := log.FirstSeq()
	last, ok2 := log.LastSeq()
	if !ok1 || !ok2 {
		t.Fatalf("log empty after %d appends (first ok=%v, last ok=%v)", N, ok1, ok2)
	}
	if got := last - first + 1; int(got) != N {
		t.Fatalf("log length = %d, want %d (first=%d last=%d)", got, N, first, last)
	}

	prev := uint64(0)
	for i := 0; i < N; i++ {
		select {
		case e := <-ch:
			if e.Seq != prev+1 {
				t.Fatalf("entry[%d] seq = %d, want %d", i, e.Seq, prev+1)
			}
			prev = e.Seq
			mu, ok := e.Op.(*pb.Mutation)
			if !ok {
				t.Fatalf("entry[%d] op type = %T, want *pb.Mutation", i, e.Op)
			}
			if mu.GetOp().GetPutVertices() == nil {
				t.Fatalf("entry[%d] missing PutVertices oneof", i)
			}
			if len(mu.GetOrigin()) != len(hlc.NodeID{}) {
				t.Fatalf("entry[%d] origin len = %d, want %d",
					i, len(mu.GetOrigin()), len(hlc.NodeID{}))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for entry %d", i)
		}
	}
}

// TestLanternService_MutationLog_NotWired_NoOp guards the test path where
// the service is built without WithReplication: write RPCs must still
// succeed and not panic on the nil log/clock.
func TestLanternService_MutationLog_NotWired_NoOp(t *testing.T) {
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute))
	if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "k"}},
	}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
}

func keyFor(i int) string {
	return "k-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
