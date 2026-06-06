package service_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// recordingMetrics counts metric callbacks so tests can assert on them.
type recordingMetrics struct {
	started int64
	ended   int64
	dropped map[string]int64
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{dropped: map[string]int64{}}
}

func (m *recordingMetrics) OnSubscribeStarted() { atomic.AddInt64(&m.started, 1) }
func (m *recordingMetrics) OnSubscribeEnded()   { atomic.AddInt64(&m.ended, 1) }
func (m *recordingMetrics) OnSubscribeDropped(reason string) {
	m.dropped[reason]++
}

func newReplicationServer(t *testing.T, log *mutationlog.Log, m service.SubscribeMetrics) (pb.LanternReplicationServiceClient, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	repl := service.NewLanternReplicationService(log, nil, nil).WithMetrics(m)
	pb.RegisterLanternReplicationServiceServer(srv, repl)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		srv.Stop()
		t.Fatalf("grpc.NewClient: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		srv.Stop()
	}
	return pb.NewLanternReplicationServiceClient(conn), cleanup
}

// appendMutation appends a single Mutation to the log so subscribers see it.
// The Mutation mirrors what LanternService.logMutation would produce so the
// shape on the wire matches production.
func appendMutation(t *testing.T, log *mutationlog.Log, clock *hlc.Clock, key string) {
	t.Helper()
	ts := clock.Now()
	id := ts.NodeID
	mu := &pb.Mutation{
		Hlc: &pb.HLCTimestamp{
			WallNs:  ts.WallNs,
			Logical: ts.Logical,
			NodeId:  append([]byte(nil), id[:]...),
		},
		Origin: append([]byte(nil), id[:]...),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
			PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: key}}},
		}},
	}
	if _, err := log.Append(mu, ts); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// TestSubscribe_StreamsInOrder verifies the happy path: subscribe from 0,
// burst N writes, receive N responses with strictly monotone Seq starting
// at 1 and the originating mutation payload intact.
func TestSubscribe_StreamsInOrder(t *testing.T) {
	const N = 50

	log := mutationlog.New(mutationlog.Options{Capacity: 2 * N, SubscriberBuffer: 2 * N})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x01, 0x02, 0x03}, hlc.Options{})

	metrics := newRecordingMetrics()
	client, cleanup := newReplicationServer(t, log, metrics)
	t.Cleanup(cleanup)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Append first, then subscribe from seq=1. Subscribing first with
	// fromSeq=0 races the handler against the appender: the log treats
	// fromSeq < firstSeq as gapped, so a slow handler that registers
	// after the first append would erroneously bail. Append-then-subscribe
	// is also the realistic CDC path (consumer resumes from a checkpoint).
	for i := 0; i < N; i++ {
		appendMutation(t, log, clock, "k"+itoa(i))
	}

	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{FromSeq: 1})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	var prev uint64
	for i := 0; i < N; i++ {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		got := resp.GetMutation()
		if got.GetSeq() != prev+1 {
			t.Fatalf("entry[%d] seq=%d want %d", i, got.GetSeq(), prev+1)
		}
		prev = got.GetSeq()
		if got.GetOp().GetPutVertices() == nil {
			t.Fatalf("entry[%d] missing PutVertices oneof", i)
		}
		if got.GetHlc() == nil {
			t.Fatalf("entry[%d] missing HLC", i)
		}
	}

	if atomic.LoadInt64(&metrics.started) != 1 {
		t.Errorf("started counter = %d, want 1", metrics.started)
	}
}

// TestSubscribe_GappedReturnsFailedPrecondition: when fromSeq is older than
// the ring's firstSeq the server immediately returns FailedPrecondition so
// the client knows to snapshot + resubscribe (RFC §8.2).
func TestSubscribe_GappedReturnsFailedPrecondition(t *testing.T) {
	// Capacity=2 so a burst of 3 evicts seq=1.
	log := mutationlog.New(mutationlog.Options{Capacity: 2, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{}, hlc.Options{})
	for i := 0; i < 3; i++ {
		appendMutation(t, log, clock, "k"+itoa(i))
	}

	metrics := newRecordingMetrics()
	client, cleanup := newReplicationServer(t, log, metrics)
	t.Cleanup(cleanup)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{FromSeq: 1})
	if err != nil {
		t.Fatalf("Subscribe call: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("Recv returned nil error; want FailedPrecondition")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status: %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("code = %s, want FailedPrecondition", st.Code())
	}
	if got := metrics.dropped["gapped"]; got != 1 {
		t.Errorf("dropped[gapped] = %d, want 1", got)
	}
}

// TestSubscribe_ClientCancel cleans up server-side resources on client
// cancellation. We rely on the active-streams counter returning to 0.
func TestSubscribe_ClientCancel(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 64, SubscriberBuffer: 64})
	t.Cleanup(func() { _ = log.Close() })

	metrics := newRecordingMetrics()
	client, cleanup := newReplicationServer(t, log, metrics)
	t.Cleanup(cleanup)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{FromSeq: 0})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// Give the handler a moment to register its subscription.
	time.Sleep(50 * time.Millisecond)
	cancel()
	_, err = stream.Recv()
	if err == nil || (!errors.Is(err, context.Canceled) && status.Code(err) != codes.Canceled) {
		// EOF is also acceptable if the server tore down first.
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Recv after cancel: got %v, want Canceled or EOF", err)
		}
	}
	// The handler's defer must have flipped the gauge back.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&metrics.ended) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ended counter = %d, want 1", metrics.ended)
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
