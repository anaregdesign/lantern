package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/grpc"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// ConnectOption configures the additive Connect-backed Lantern constructor.
// It is intentionally NOT compatible with the existing Option type — the
// underlying transports take fundamentally different knobs (http.Client vs
// grpc.DialOption), and overloading one shape across two transports would
// produce confusing runtime behavior at every WithXxx site.
type ConnectOption func(*connectOptions)

type connectOptions struct {
	httpClient     *http.Client
	clientOptions  []connect.ClientOption
	defaultTimeout int64 // nanoseconds; stored as int64 to skip the time import.
	batchChunkSize int
}

// WithHTTPClient supplies the http.Client used by the Connect transport.
// When omitted, NewLanternConnect builds an h2c-capable client via
// defaultH2CClient (HTTP/2 over plaintext) so the SDK works out of the box
// against the server's additive Connect listener.
//
// Pass a TLS-configured http.Client for production: build an
// http2.Transport with a real *tls.Config and supply it via
// &http.Client{Transport: ...}. The Connect protocol does NOT require h2c —
// HTTPS is the recommended production transport.
func WithHTTPClient(c *http.Client) ConnectOption {
	return func(o *connectOptions) { o.httpClient = c }
}

// WithConnectClientOption forwards arbitrary connect.ClientOption values
// (interceptors, codec selection, gzip compression, etc.) to the generated
// Connect client constructor. Callers needing the gRPC wire protocol pass
// connect.WithGRPC() here; callers wanting OTel pass an OTel interceptor.
func WithConnectClientOption(opts ...connect.ClientOption) ConnectOption {
	return func(o *connectOptions) { o.clientOptions = append(o.clientOptions, opts...) }
}

// WithConnectBatchChunkSize mirrors WithBatchChunkSize for the Connect-
// backed client. Must be > 0; otherwise the default of 1000 is kept.
func WithConnectBatchChunkSize(n int) ConnectOption {
	return func(o *connectOptions) {
		if n > 0 {
			o.batchChunkSize = n
		}
	}
}

// NewLanternConnect returns a Lantern client backed by the Connect-Go
// transport. baseURL must include the scheme: `http://host:port` for h2c
// (matches LANTERN_CONNECT_PORT default), or `https://host` for TLS.
//
// This constructor is the **additive** preview of the future v1.0 transport
// (see #335, #337, #338). It coexists with the existing grpc-backed
// NewLantern / NewLanternWithEndpoints constructors; both return the same
// *Lantern type, so downstream code that holds a *Lantern is transport-
// agnostic. Pick the constructor that matches your wire-side reality:
//
//   - NewLanternConnect("http://lantern:6381") — server with
//     LANTERN_CONNECT_PORT=6381 (additive Connect listener from #337).
//   - NewLantern("lantern:6380") — server with the primary gRPC port
//     (default until #347 cuts over).
//
// baseURL must not end with a trailing slash; the Connect-Go client
// constructs paths by concatenation and a trailing slash would produce
// `//graph.v1.LanternService/...` URLs that the server rejects. A trailing
// slash supplied by the caller is stripped defensively.
func NewLanternConnect(baseURL string, opts ...ConnectOption) (*Lantern, error) {
	if baseURL == "" {
		return nil, errors.New("client: NewLanternConnect requires a base URL with scheme")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("client: NewLanternConnect baseURL must start with http:// or https://; got %q", baseURL)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	co := connectOptions{
		batchChunkSize: defaultBatchChunkSize,
	}
	for _, apply := range opts {
		apply(&co)
	}
	if co.httpClient == nil {
		co.httpClient = defaultH2CClient()
	}

	cc := graphv1connect.NewLanternServiceClient(co.httpClient, baseURL, co.clientOptions...)
	return &Lantern{
		conn:   nil, // No grpc.ClientConn on this transport path.
		client: &connectClientAdapter{svc: cc},
		opts: options{
			batchChunkSize:    co.batchChunkSize,
			serviceConfigJSON: "", // unused by Connect transport
		},
		connectHTTPClient: co.httpClient,
		connectBaseURL:    baseURL,
	}, nil
}

// connectClientAdapter implements pb.LanternServiceClient on top of the
// generated Connect-Go client. This is the single seam that lets every
// existing *Lantern method (GetVertex, PutVertices, AddEdges, Illuminate,
// ...) keep its grpc-style signature while the underlying transport is
// Connect.
//
// The methods accept `...grpc.CallOption` to satisfy the generated
// interface but IGNORE them — Connect-Go has its own per-call options
// (request headers, deadlines via context) that callers configure via
// WithConnectClientOption at construction time. *Lantern's own helpers
// never pass call options to these methods, so the ignored variadic is
// invisible to consumers; direct users of the underlying pb client
// interface should construct the Connect client directly.
type connectClientAdapter struct {
	svc graphv1connect.LanternServiceClient
}

// connectUnary is the one-line forwarding helper every adapter method
// uses. Generics keep each method body to a single line while preserving
// type safety: the call site supplies the typed underlying Connect-Go
// method, and the wrapper handles request/response boxing plus error
// translation.
func connectUnary[Req, Resp any](
	ctx context.Context,
	req *Req,
	fn func(context.Context, *connect.Request[Req]) (*connect.Response[Resp], error),
) (*Resp, error) {
	resp, err := fn(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, connectErrToGRPC(err)
	}
	return resp.Msg, nil
}

// connectErrToGRPC repackages a Connect error into a gRPC status error
// carrying the equivalent code, so the existing wrapStatus shim in
// client.go (and any consumer's `status.Code(err)` call) keep returning
// the same canonical codes after the transport switch.
//
// Defined in connect_errors.go so the code-mapping table sits with the
// rest of the gRPC interop bridge.

func (a *connectClientAdapter) Illuminate(ctx context.Context, req *pb.IlluminateRequest, _ ...grpc.CallOption) (*pb.IlluminateResponse, error) {
	return connectUnary(ctx, req, a.svc.Illuminate)
}
func (a *connectClientAdapter) GetVertex(ctx context.Context, req *pb.GetVertexRequest, _ ...grpc.CallOption) (*pb.GetVertexResponse, error) {
	return connectUnary(ctx, req, a.svc.GetVertex)
}
func (a *connectClientAdapter) GetVertices(ctx context.Context, req *pb.GetVerticesRequest, _ ...grpc.CallOption) (*pb.GetVerticesResponse, error) {
	return connectUnary(ctx, req, a.svc.GetVertices)
}
func (a *connectClientAdapter) PutVertex(ctx context.Context, req *pb.PutVertexRequest, _ ...grpc.CallOption) (*pb.PutVertexResponse, error) {
	return connectUnary(ctx, req, a.svc.PutVertex)
}
func (a *connectClientAdapter) PutVertices(ctx context.Context, req *pb.PutVerticesRequest, _ ...grpc.CallOption) (*pb.PutVerticesResponse, error) {
	return connectUnary(ctx, req, a.svc.PutVertices)
}
func (a *connectClientAdapter) DeleteVertex(ctx context.Context, req *pb.DeleteVertexRequest, _ ...grpc.CallOption) (*pb.DeleteVertexResponse, error) {
	return connectUnary(ctx, req, a.svc.DeleteVertex)
}
func (a *connectClientAdapter) DeleteVertices(ctx context.Context, req *pb.DeleteVerticesRequest, _ ...grpc.CallOption) (*pb.DeleteVerticesResponse, error) {
	return connectUnary(ctx, req, a.svc.DeleteVertices)
}
func (a *connectClientAdapter) ScanVertices(ctx context.Context, req *pb.ScanVerticesRequest, _ ...grpc.CallOption) (*pb.ScanVerticesResponse, error) {
	return connectUnary(ctx, req, a.svc.ScanVertices)
}
func (a *connectClientAdapter) CountVerticesByPrefix(ctx context.Context, req *pb.CountVerticesByPrefixRequest, _ ...grpc.CallOption) (*pb.CountVerticesByPrefixResponse, error) {
	return connectUnary(ctx, req, a.svc.CountVerticesByPrefix)
}
func (a *connectClientAdapter) DeleteVerticesByPrefix(ctx context.Context, req *pb.DeleteVerticesByPrefixRequest, _ ...grpc.CallOption) (*pb.DeleteVerticesByPrefixResponse, error) {
	return connectUnary(ctx, req, a.svc.DeleteVerticesByPrefix)
}
func (a *connectClientAdapter) GetEdge(ctx context.Context, req *pb.GetEdgeRequest, _ ...grpc.CallOption) (*pb.GetEdgeResponse, error) {
	return connectUnary(ctx, req, a.svc.GetEdge)
}
func (a *connectClientAdapter) GetEdges(ctx context.Context, req *pb.GetEdgesRequest, _ ...grpc.CallOption) (*pb.GetEdgesResponse, error) {
	return connectUnary(ctx, req, a.svc.GetEdges)
}
func (a *connectClientAdapter) AddEdge(ctx context.Context, req *pb.AddEdgeRequest, _ ...grpc.CallOption) (*pb.AddEdgeResponse, error) {
	return connectUnary(ctx, req, a.svc.AddEdge)
}
func (a *connectClientAdapter) AddEdges(ctx context.Context, req *pb.AddEdgesRequest, _ ...grpc.CallOption) (*pb.AddEdgesResponse, error) {
	return connectUnary(ctx, req, a.svc.AddEdges)
}
func (a *connectClientAdapter) PutEdge(ctx context.Context, req *pb.PutEdgeRequest, _ ...grpc.CallOption) (*pb.PutEdgeResponse, error) {
	return connectUnary(ctx, req, a.svc.PutEdge)
}
func (a *connectClientAdapter) PutEdges(ctx context.Context, req *pb.PutEdgesRequest, _ ...grpc.CallOption) (*pb.PutEdgesResponse, error) {
	return connectUnary(ctx, req, a.svc.PutEdges)
}
func (a *connectClientAdapter) DeleteEdge(ctx context.Context, req *pb.DeleteEdgeRequest, _ ...grpc.CallOption) (*pb.DeleteEdgeResponse, error) {
	return connectUnary(ctx, req, a.svc.DeleteEdge)
}
func (a *connectClientAdapter) DeleteEdges(ctx context.Context, req *pb.DeleteEdgesRequest, _ ...grpc.CallOption) (*pb.DeleteEdgesResponse, error) {
	return connectUnary(ctx, req, a.svc.DeleteEdges)
}
func (a *connectClientAdapter) ScanEdges(ctx context.Context, req *pb.ScanEdgesRequest, _ ...grpc.CallOption) (*pb.ScanEdgesResponse, error) {
	return connectUnary(ctx, req, a.svc.ScanEdges)
}
func (a *connectClientAdapter) GetServerStatus(ctx context.Context, req *pb.GetServerStatusRequest, _ ...grpc.CallOption) (*pb.GetServerStatusResponse, error) {
	return connectUnary(ctx, req, a.svc.GetServerStatus)
}
func (a *connectClientAdapter) GetReplicationStatus(ctx context.Context, req *pb.GetReplicationStatusRequest, _ ...grpc.CallOption) (*pb.GetReplicationStatusResponse, error) {
	return connectUnary(ctx, req, a.svc.GetReplicationStatus)
}

// SubscribeConnect opens a server-stream against the replication service
// over the Connect transport and returns an iter.Seq2 yielding successive
// *SubscribeResponse frames until the stream closes. fromSeq passes
// through unchanged.
//
// Stop conditions: clean EOF, any server error, the supplied ctx being
// canceled, or the consumer returning false from yield. The underlying
// *connect.ServerStreamForClient is always closed before iteration ends.
//
// This method lives on *Lantern (not the inner adapter) because the grpc
// path historically exposes Subscribe via the pb client directly and the
// Connect path's ergonomic surface is the iterator shape; mixing both on
// the same client interface would force a transport-specific surface back
// onto every consumer of the existing grpc-backed *Lantern. The grpc-
// backed *Lantern returns an error here so misuse fails loudly rather
// than silently no-op'ing.
func (l *Lantern) SubscribeConnect(ctx context.Context, fromSeq uint64) iter.Seq2[*pb.SubscribeResponse, error] {
	return func(yield func(*pb.SubscribeResponse, error) bool) {
		if l.connectHTTPClient == nil || l.connectBaseURL == "" {
			yield(nil, errors.New("client: SubscribeConnect requires NewLanternConnect; this *Lantern was built via the grpc path"))
			return
		}
		rc := graphv1connect.NewLanternReplicationServiceClient(l.connectHTTPClient, l.connectBaseURL)
		stream, err := rc.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromSeq: fromSeq}))
		if err != nil {
			yield(nil, connectErrToGRPC(err))
			return
		}
		defer func() { _ = stream.Close() }()
		for stream.Receive() {
			if !yield(stream.Msg(), nil) {
				return
			}
		}
		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			yield(nil, connectErrToGRPC(err))
		}
	}
}

// PingConnect issues a plain GET against {metricsURL}/healthz. Returns
// nil iff the server responds 200.
//
// Unlike the legacy gRPC-Health-based Ping, this probe lives entirely
// over HTTP/1.1 and does not depend on the Connect baseURL — health
// checks are owned by the metrics server (default :9090), not the
// Connect listener. Pass http://host:9090 (or wherever
// LANTERN_METRICS_ADDR points). nil httpClient defaults to
// http.DefaultClient — the metrics server speaks plain HTTP/1.1, so
// the h2c-flavored httpClient stored on *Lantern is not required.
func PingConnect(ctx context.Context, httpClient *http.Client, metricsURL string) error {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(metricsURL, "/")+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("client: PingConnect %s returned %d", req.URL.String(), resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
