package integration_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"connectrpc.com/connect"
	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// TestSubscribe_E2E_100Writes wires a real LanternService +
// LanternReplicationService through the Connect-on-h2c httptest
// harness, drives 100 PutVertex writes through the SDK, and asserts
// the subscriber sees all 100 mutations in strict Seq order with
// PutVertices payloads intact. Acceptance criterion for issue #180.
func TestSubscribe_E2E_100Writes(t *testing.T) {
	const N = 100

	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})

	log := mutationlog.New(mutationlog.Options{Capacity: 4 * N, SubscriberBuffer: 4 * N})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0xAA, 0xBB}, hlc.Options{})

	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)

	svc := service.NewLanternService(cache).
		WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock)
	srv := newConnectTestServer(t, svc, rep, vi.ConnectInterceptor())

	// Subscriber: raw Connect-Go replication client.
	subCli := newReplicationRawClient(t, srv.url)

	// Writer (SDK Connect transport).
	l := newConnectClientFor(t, srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Issue writes first so Subscribe can replay them from the
	// in-memory ring. SubscriberBuffer is sized to fit all entries.
	for i := 0; i < N; i++ {
		if err := l.PutVertex(ctx, "k-"+itoa(i), "v", time.Minute); err != nil {
			t.Fatalf("PutVertex[%d]: %v", i, err)
		}
	}

	stream, err := subCli.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromSeq: 1}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	var prev uint64
	for i := 0; i < N; i++ {
		if !stream.Receive() {
			if streamErr := stream.Err(); streamErr != nil && !errors.Is(streamErr, io.EOF) {
				t.Fatalf("Recv[%d]: %v", i, streamErr)
			}
			t.Fatalf("stream ended at %d/%d", i, N)
		}
		resp := stream.Msg()
		got := resp.GetMutation()
		if got.GetSeq() != prev+1 {
			t.Fatalf("entry[%d] seq=%d want %d", i, got.GetSeq(), prev+1)
		}
		prev = got.GetSeq()
		pv := got.GetOp().GetPutVertices()
		if pv == nil || len(pv.GetVertices()) != 1 {
			t.Fatalf("entry[%d] missing PutVertices payload", i)
		}
		if want := "k-" + itoa(i); pv.GetVertices()[0].GetKey() != want {
			t.Errorf("entry[%d] key=%q want %q", i, pv.GetVertices()[0].GetKey(), want)
		}
	}
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
