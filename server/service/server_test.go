package service

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// blockingStream is a server-side stream handler that blocks until the
// stream's context is canceled. Used to force GracefulStop to hang so the
// timeout branch of gracefulShutdown is exercised.
func blockingStream(_ any, stream grpc.ServerStream) error {
	<-stream.Context().Done()
	return stream.Context().Err()
}

// TestLanternServer_gracefulShutdown_TimeoutForcesStop pins an in-flight
// stream open so GracefulStop cannot drain, then asserts gracefulShutdown
// honors ShutdownTimeout by escalating to Stop instead of blocking forever.
func TestLanternServer_gracefulShutdown_TimeoutForcesStop(t *testing.T) {
	lis := bufconn.Listen(1 << 16)
	grpcSrv := grpc.NewServer()

	fakeDesc := grpc.ServiceDesc{
		ServiceName: "fake.GracefulShutdownService",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "Block",
			Handler:       blockingStream,
			ClientStreams: true,
			ServerStreams: true,
		}},
		Metadata: "graceful_shutdown_test.go",
	}
	grpcSrv.RegisterService(&fakeDesc, struct{}{})

	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	clientDesc := &grpc.StreamDesc{
		StreamName:    "Block",
		ClientStreams: true,
		ServerStreams: true,
	}
	stream, err := conn.NewStream(streamCtx, clientDesc, "/fake.GracefulShutdownService/Block")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	// Send one frame so the server-side handler has actually been entered;
	// GracefulStop only waits on in-flight RPCs.
	if err := stream.SendMsg(&emptypb{}); err != nil {
		t.Fatalf("SendMsg: %v", err)
	}

	s := &LanternServer{
		server:          grpcSrv,
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		shutdownTimeout: 200 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // gracefulShutdown waits on ctx.Done, so pre-cancel

	start := time.Now()
	s.gracefulShutdown(ctx)
	elapsed := time.Since(start)

	if elapsed < s.shutdownTimeout {
		t.Fatalf("gracefulShutdown returned before timeout: %v < %v", elapsed, s.shutdownTimeout)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("gracefulShutdown blocked far past timeout: %v", elapsed)
	}

	// Serve must have returned after Stop was forced.
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("grpc.Server.Serve did not return after forced Stop")
	}
}

// emptypb is a zero-byte proto-compatible message we can SendMsg without
// pulling in a real proto type — grpc only needs Marshal/Unmarshal.
type emptypb struct{}

func (*emptypb) Reset()         {}
func (*emptypb) String() string { return "" }
func (*emptypb) ProtoMessage()  {}
