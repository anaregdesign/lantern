package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// newOptsClient stands up a Connect-on-h2c httptest server with the
// supplied validation limits + per-test extra Connect interceptors,
// then returns a *client.Lantern speaking Connect against it.
//
// Replaces the legacy bufconn + grpc.NewServer harness retired with
// the Connect-only SDK collapse (#367). The Connect interceptor list
// is variadic so tests can layer in failure-injection / counting /
// retry-probe behaviour without each spinning up its own helper.
func newOptsClient(
	t *testing.T,
	lim provider.ValidationLimits,
	extra ...connect.Interceptor,
) *client.Lantern {
	t.Helper()
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(lim)
	interceptors := append([]connect.Interceptor{val.ConnectInterceptor()}, extra...)
	srv := newConnectTestServer(t, svc, nil, interceptors...)
	return newConnectClientFor(t, srv.url)
}

// newOptsClientWithCustomOptions is the same harness but lets the
// test pass extra SDK-side Options (WithDefaultTimeout, etc.).
func newOptsClientWithCustomOptions(
	t *testing.T,
	lim provider.ValidationLimits,
	clientOpts []client.Option,
	extra ...connect.Interceptor,
) *client.Lantern {
	t.Helper()
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(lim)
	interceptors := append([]connect.Interceptor{val.ConnectInterceptor()}, extra...)
	srv := newConnectTestServer(t, svc, nil, interceptors...)
	all := append([]client.Option{client.WithHTTPClient(h2cClient())}, clientOpts...)
	l, err := client.NewLanternConnect(srv.url, all...)
	if err != nil {
		t.Fatalf("NewLanternConnect: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestClient_GetVertex_NotFoundWrapped(t *testing.T) {
	c := newOptsClient(t, provider.ValidationLimits{MaxKeyLen: 64, MaxBatchSize: 100})
	_, err := c.GetVertex(context.Background(), "absent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("error %v should wrap ErrNotFound", err)
	}
}

func TestClient_ValidationInterceptor_RejectsLongKey(t *testing.T) {
	c := newOptsClient(t, provider.ValidationLimits{MaxKeyLen: 4, MaxBatchSize: 10})
	err := c.PutVertex(context.Background(), "way-too-long-key", 1, time.Minute)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Errorf("error %v should wrap ErrInvalidArgument", err)
	}
}

func TestClient_BatchChunking(t *testing.T) {
	// MaxBatchSize=5 on the server would reject an 8-element put.
	// Chunking at 3 keeps every request under the cap.
	c := newOptsClientWithCustomOptions(t,
		provider.ValidationLimits{MaxKeyLen: 32, MaxBatchSize: 5},
		[]client.Option{client.WithBatchChunkSize(3)},
	)

	ctx := context.Background()
	inputs := make([]client.VertexInput, 0, 8)
	for i := 0; i < 8; i++ {
		inputs = append(inputs, client.VertexInput{
			Key:        fmt.Sprintf("k%d", i),
			Value:      int64(i),
			Expiration: time.Now().Add(time.Minute),
		})
	}
	if err := c.PutVertices(ctx, inputs); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := c.GetVertex(ctx, fmt.Sprintf("k%d", i)); err != nil {
			t.Errorf("missing k%d after chunked put: %v", i, err)
		}
	}
}

// TestClient_DefaultTimeoutHonoured asserts that WithDefaultTimeout
// applies a context deadline to RPCs that arrive without one. A
// server-side interceptor blocks until ctx cancellation so the
// timeout is the only thing that can unstick the call.
func TestClient_DefaultTimeoutHonoured(t *testing.T) {
	blocker := connect.UnaryInterceptorFunc(func(_ connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			<-ctx.Done()
			return nil, connect.NewError(connect.CodeDeadlineExceeded, ctx.Err())
		}
	})
	c := newOptsClientWithCustomOptions(t,
		provider.ValidationLimits{MaxKeyLen: 64, MaxBatchSize: 10},
		[]client.Option{client.WithDefaultTimeout(100 * time.Millisecond)},
		blocker,
	)
	start := time.Now()
	_, err := c.GetVertex(context.Background(), "k")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected DeadlineExceeded, got nil")
	}
	// The SDK's connectErrToGRPC shim re-encodes Connect errors as
	// google.golang.org/grpc/status errors so existing call-sites
	// keep using status.Code(err) / codes.X. Accept either the
	// gRPC code, the Connect code (for direct connect errors that
	// slipped through), or context.DeadlineExceeded (for the local
	// timeout path).
	if status.Code(err) != codes.DeadlineExceeded &&
		connect.CodeOf(err) != connect.CodeDeadlineExceeded &&
		!errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v, want DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("default timeout did not fire promptly: %v", elapsed)
	}
}

func TestClient_RespectsContextCancel(t *testing.T) {
	c := newOptsClient(t, provider.ValidationLimits{MaxKeyLen: 64, MaxBatchSize: 10})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetVertex(ctx, "k")
	if err == nil {
		t.Fatal("expected ctx cancel error, got nil")
	}
	if !errors.Is(err, context.Canceled) &&
		connect.CodeOf(err) != connect.CodeCanceled &&
		status.Code(err) != codes.Canceled {
		t.Errorf("error %v, want Canceled", err)
	}
}

// silenceUnused keeps the imports referenced when individual tests
// above are commented out during local debugging.
var (
	_ = httptest.NewUnstartedServer
	_ = http.MethodPost
	_ = graphv1connect.LanternServiceName
)
