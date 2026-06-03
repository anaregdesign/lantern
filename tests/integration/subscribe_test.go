package integration_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// TestSubscribe_E2E_100Writes wires a real LanternService + LanternReplicationService
// over bufconn, drives 100 PutVertex writes through the SDK, and asserts the
// subscriber sees all 100 mutations in strict Seq order with PutVertices
// payloads intact. This is the acceptance criterion for issue #180.
func TestSubscribe_E2E_100Writes(t *testing.T) {
	const N = 100

	lis := bufconn.Listen(1 << 16)
	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})
	srv := grpc.NewServer(grpc.UnaryInterceptor(vi.UnaryServerInterceptor()))

	log := mutationlog.New(mutationlog.Options{Capacity: 4 * N, SubscriberBuffer: 4 * N})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0xAA, 0xBB}, hlc.Options{})

	svc := service.NewLanternService(cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithReplication(log, clock, nil)
	pb.RegisterLanternServiceServer(srv, svc)
	pb.RegisterLanternReplicationServiceServer(srv, service.NewLanternReplicationService(log))

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }

	// Subscriber connection (raw gRPC, since the SDK doesn't expose Subscribe).
	subConn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		t.Fatalf("NewClient(sub): %v", err)
	}
	defer subConn.Close()
	subCli := pb.NewLanternReplicationServiceClient(subConn)

	// Writer (SDK).
	l, err := client.NewLantern(
		"passthrough://bufconn",
		client.WithTransportCredentials(insecure.NewCredentials()),
		client.WithDialOption(grpc.WithContextDialer(dialer)),
	)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Issue writes first so Subscribe can replay them from the in-memory
	// ring. SubscriberBuffer is sized to fit all entries.
	for i := 0; i < N; i++ {
		if err := l.PutVertex(ctx, "k-"+itoa(i), "v", time.Minute); err != nil {
			t.Fatalf("PutVertex[%d]: %v", i, err)
		}
	}

	stream, err := subCli.Subscribe(ctx, &pb.SubscribeRequest{FromSeq: 1})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	var prev uint64
	for i := 0; i < N; i++ {
		resp, err := stream.Recv()
		if err == io.EOF {
			t.Fatalf("stream EOF at %d/%d", i, N)
		}
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
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
