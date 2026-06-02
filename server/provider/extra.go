package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math"
	"os"

	pb "github.com/anaregdesign/lantern/gen/go/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// NewLifecycleConfig forwards Config knobs into the service-layer lifecycle
// struct. Kept narrow so service.go doesn't need to import provider.
func NewLifecycleConfig(c *Config) service.LifecycleConfig {
	return service.LifecycleConfig{
		GCInterval:      c.GCInterval,
		ShutdownTimeout: c.ShutdownTimeout,
	}
}

// ---------------------------------------------------------------------------
// Request validation
// ---------------------------------------------------------------------------

// ValidationLimits caps user-controllable request dimensions so a single
// caller can't exhaust server resources before the rate limiter or message
// size cap kicks in.
type ValidationLimits struct {
	MaxKeyLen         int
	MaxBatchSize      int
	IlluminateMaxStep int
	IlluminateMaxK    int
}

type ValidationInterceptor struct {
	limits ValidationLimits
}

func NewValidationInterceptor(l ValidationLimits) *ValidationInterceptor {
	return &ValidationInterceptor{limits: l}
}

func (v *ValidationInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := v.validate(req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func (v *ValidationInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	// Lantern has no streaming RPCs yet; the wrapper is registered so future
	// streams pick up validation without re-wiring the server.
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, ss)
	}
}

func (v *ValidationInterceptor) validate(req any) error {
	switch r := req.(type) {
	case *pb.GetVertexRequest:
		return v.checkKey("key", r.GetKey())
	case *pb.DeleteVertexRequest:
		return v.checkKey("key", r.GetKey())
	case *pb.DeleteVerticesRequest:
		if err := v.checkBatch(len(r.GetKeys())); err != nil {
			return err
		}
		for i, k := range r.GetKeys() {
			if err := v.checkKey(fmt.Sprintf("keys[%d]", i), k); err != nil {
				return err
			}
		}
	case *pb.PutVertexRequest:
		if err := v.checkBatch(len(r.GetVertices())); err != nil {
			return err
		}
		for i, vx := range r.GetVertices() {
			if vx == nil {
				return status.Errorf(codes.InvalidArgument, "vertices[%d] is nil", i)
			}
			if err := v.checkKey(fmt.Sprintf("vertices[%d].key", i), vx.GetKey()); err != nil {
				return err
			}
		}
	case *pb.GetEdgeRequest:
		if err := v.checkKey("tail", r.GetTail()); err != nil {
			return err
		}
		return v.checkKey("head", r.GetHead())
	case *pb.DeleteEdgeRequest:
		if err := v.checkKey("tail", r.GetTail()); err != nil {
			return err
		}
		return v.checkKey("head", r.GetHead())
	case *pb.DeleteEdgesRequest:
		if err := v.checkBatch(len(r.GetEdges())); err != nil {
			return err
		}
		for i, e := range r.GetEdges() {
			if e == nil {
				return status.Errorf(codes.InvalidArgument, "edges[%d] is nil", i)
			}
			if err := v.checkKey(fmt.Sprintf("edges[%d].tail", i), e.GetTail()); err != nil {
				return err
			}
			if err := v.checkKey(fmt.Sprintf("edges[%d].head", i), e.GetHead()); err != nil {
				return err
			}
		}
	case *pb.AddEdgeRequest:
		return v.validateEdges(r.GetEdges())
	case *pb.PutEdgeRequest:
		return v.validateEdges(r.GetEdges())
	case *pb.IlluminateRequest:
		if err := v.checkKey("seed", r.GetSeed()); err != nil {
			return err
		}
		if v.limits.IlluminateMaxStep > 0 && int(r.GetStep()) > v.limits.IlluminateMaxStep {
			return status.Errorf(codes.InvalidArgument, "step %d exceeds max %d", r.GetStep(), v.limits.IlluminateMaxStep)
		}
		if v.limits.IlluminateMaxK > 0 && int(r.GetK()) > v.limits.IlluminateMaxK {
			return status.Errorf(codes.InvalidArgument, "k %d exceeds max %d", r.GetK(), v.limits.IlluminateMaxK)
		}
	}
	return nil
}

func (v *ValidationInterceptor) validateEdges(edges []*pb.Edge) error {
	if err := v.checkBatch(len(edges)); err != nil {
		return err
	}
	for i, e := range edges {
		if e == nil {
			return status.Errorf(codes.InvalidArgument, "edges[%d] is nil", i)
		}
		if err := v.checkKey(fmt.Sprintf("edges[%d].tail", i), e.GetTail()); err != nil {
			return err
		}
		if err := v.checkKey(fmt.Sprintf("edges[%d].head", i), e.GetHead()); err != nil {
			return err
		}
		w := float64(e.GetWeight())
		if math.IsNaN(w) || math.IsInf(w, 0) {
			return status.Errorf(codes.InvalidArgument, "edges[%d].weight must be finite, got %v", i, e.GetWeight())
		}
	}
	return nil
}

func (v *ValidationInterceptor) checkKey(field, value string) error {
	if value == "" {
		return status.Errorf(codes.InvalidArgument, "%s must not be empty", field)
	}
	if v.limits.MaxKeyLen > 0 && len(value) > v.limits.MaxKeyLen {
		return status.Errorf(codes.InvalidArgument, "%s length %d exceeds max %d", field, len(value), v.limits.MaxKeyLen)
	}
	return nil
}

func (v *ValidationInterceptor) checkBatch(n int) error {
	if n == 0 {
		return status.Error(codes.InvalidArgument, "request batch must not be empty")
	}
	if v.limits.MaxBatchSize > 0 && n > v.limits.MaxBatchSize {
		return status.Errorf(codes.InvalidArgument, "batch size %d exceeds max %d", n, v.limits.MaxBatchSize)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Rate limiting
// ---------------------------------------------------------------------------

// RateLimitInterceptor enforces a process-wide token bucket. Requests beyond
// the bucket return codes.ResourceExhausted so well-behaved clients (and the
// SDK's default retry policy) can back off.
type RateLimitInterceptor struct {
	lim *rate.Limiter
}

func NewRateLimitInterceptor(rps float64, burst int) *RateLimitInterceptor {
	if burst <= 0 {
		burst = int(rps)
		if burst <= 0 {
			burst = 1
		}
	}
	return &RateLimitInterceptor{lim: rate.NewLimiter(rate.Limit(rps), burst)}
}

func (r *RateLimitInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !r.lim.Allow() {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

func (r *RateLimitInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !r.lim.Allow() {
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, ss)
	}
}

// ---------------------------------------------------------------------------
// TLS
// ---------------------------------------------------------------------------

// loadServerTLS returns nil credentials (insecure) when cert/key are empty,
// TLS server credentials when both are set, or mTLS credentials when a client
// CA file is also provided.
func loadServerTLS(c *Config) (credentials.TransportCredentials, error) {
	if c.TLSCertFile == "" && c.TLSKeyFile == "" {
		if c.TLSClientCAFile != "" {
			return nil, errors.New("LANTERN_TLS_CLIENT_CA_FILE set without LANTERN_TLS_CERT_FILE / LANTERN_TLS_KEY_FILE")
		}
		return nil, nil
	}
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return nil, errors.New("LANTERN_TLS_CERT_FILE and LANTERN_TLS_KEY_FILE must both be set")
	}
	cert, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load cert/key: %w", err)
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if c.TLSClientCAFile != "" {
		caPEM, err := os.ReadFile(c.TLSClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("client CA file contained no usable certificates")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return credentials.NewTLS(cfg), nil
}
