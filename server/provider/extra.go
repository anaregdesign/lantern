package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

// NewLifecycleConfig forwards Config knobs into the service-layer lifecycle
// struct. Kept narrow so service.go doesn't need to import provider.
func NewLifecycleConfig(cache CacheConfig, shutdown ShutdownConfig) service.LifecycleConfig {
	return service.LifecycleConfig{
		GCInterval:      cache.GCInterval,
		ShutdownTimeout: shutdown.Timeout,
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
	limits     ValidationLimits
	rejectHook func(reason string)
	logger     *slog.Logger
}

func NewValidationInterceptor(l ValidationLimits) *ValidationInterceptor {
	return &ValidationInterceptor{limits: l}
}

// WithRejectHook registers a callback invoked once per rejected request
// with the canonical reason label (one of empty_key, key_too_long,
// empty_batch, batch_too_large, nil_item, bad_weight, step_too_large,
// k_too_large). Used by provider/metrics to bump
// lantern_validation_rejected_total{reason}. A nil hook disables the
// callback; safe to call exactly once during wiring.
func (v *ValidationInterceptor) WithRejectHook(f func(reason string)) *ValidationInterceptor {
	v.rejectHook = f
	return v
}

// WithLogger registers a logger that receives a debug-level "validation
// rejected" line per rejection with the canonical reason label and the
// formatted error message. Operators flip LANTERN_LOG_LEVEL=debug to
// surface field-level rejection reasons during incident triage; prod
// stays quiet at the default info level. A nil logger disables emission.
func (v *ValidationInterceptor) WithLogger(l *slog.Logger) *ValidationInterceptor {
	v.logger = l
	return v
}

// reject fires the registered hook (if any) and returns the constructed
// Connect error. Centralised so every validation rejection path is
// counted exactly once.
func (v *ValidationInterceptor) reject(reason string, format string, args ...any) error {
	if v.rejectHook != nil {
		v.rejectHook(reason)
	}
	err := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, args...))
	if v.logger != nil && v.logger.Enabled(context.Background(), slog.LevelDebug) {
		v.logger.LogAttrs(context.Background(), slog.LevelDebug, "validation rejected",
			slog.String("reason", reason),
			slog.String("error", err.Error()),
		)
	}
	return err
}

func (v *ValidationInterceptor) validate(req any) error {
	switch r := req.(type) {
	case *pb.GetVertexRequest:
		return v.checkKey("key", r.GetKey())
	case *pb.GetVerticesRequest:
		if err := v.checkBatch(len(r.GetKeys())); err != nil {
			return err
		}
		for i, k := range r.GetKeys() {
			if err := v.checkKey(fmt.Sprintf("keys[%d]", i), k); err != nil {
				return err
			}
		}
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
	case *pb.PutVerticesRequest:
		if err := v.checkBatch(len(r.GetVertices())); err != nil {
			return err
		}
		for i, vx := range r.GetVertices() {
			if vx == nil {
				return v.reject("nil_item", "vertices[%d] is nil", i)
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
	case *pb.GetEdgesRequest:
		if err := v.checkBatch(len(r.GetEdges())); err != nil {
			return err
		}
		for i, e := range r.GetEdges() {
			if e == nil {
				return v.reject("nil_item", "edges[%d] is nil", i)
			}
			if err := v.checkKey(fmt.Sprintf("edges[%d].tail", i), e.GetTail()); err != nil {
				return err
			}
			if err := v.checkKey(fmt.Sprintf("edges[%d].head", i), e.GetHead()); err != nil {
				return err
			}
		}
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
				return v.reject("nil_item", "edges[%d] is nil", i)
			}
			if err := v.checkKey(fmt.Sprintf("edges[%d].tail", i), e.GetTail()); err != nil {
				return err
			}
			if err := v.checkKey(fmt.Sprintf("edges[%d].head", i), e.GetHead()); err != nil {
				return err
			}
		}
	case *pb.AddEdgesRequest:
		return v.validateEdges(r.GetEdges())
	case *pb.PutEdgesRequest:
		return v.validateEdges(r.GetEdges())
	case *pb.IlluminateRequest:
		if err := v.checkKey("seed", r.GetSeed()); err != nil {
			return err
		}
		// Per-arm caps (#846): the oneof moved the traversal knobs into
		// per-family messages, so the limits are enforced on whichever arm
		// is selected. IlluminateMaxK caps every "how much comes back /
		// survives per hop" knob: bfs.fan_out and ppr.top_n. An unset oneof
		// carries no knobs to cap.
		switch params := r.GetParams().(type) {
		case *pb.IlluminateRequest_Bfs:
			if v.limits.IlluminateMaxStep > 0 && int(params.Bfs.GetStep()) > v.limits.IlluminateMaxStep {
				return v.reject("step_too_large", "bfs.step %d exceeds max %d", params.Bfs.GetStep(), v.limits.IlluminateMaxStep)
			}
			if v.limits.IlluminateMaxK > 0 && int(params.Bfs.GetFanOut()) > v.limits.IlluminateMaxK {
				return v.reject("k_too_large", "bfs.fan_out %d exceeds max %d", params.Bfs.GetFanOut(), v.limits.IlluminateMaxK)
			}
		case *pb.IlluminateRequest_Ppr:
			if v.limits.IlluminateMaxK > 0 && int(params.Ppr.GetTopN()) > v.limits.IlluminateMaxK {
				return v.reject("k_too_large", "ppr.top_n %d exceeds max %d", params.Ppr.GetTopN(), v.limits.IlluminateMaxK)
			}
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
			return v.reject("nil_item", "edges[%d] is nil", i)
		}
		if err := v.checkKey(fmt.Sprintf("edges[%d].tail", i), e.GetTail()); err != nil {
			return err
		}
		if err := v.checkKey(fmt.Sprintf("edges[%d].head", i), e.GetHead()); err != nil {
			return err
		}
		w := float64(e.GetWeight())
		if math.IsNaN(w) || math.IsInf(w, 0) {
			return v.reject("bad_weight", "edges[%d].weight must be finite, got %v", i, e.GetWeight())
		}
	}
	return nil
}

func (v *ValidationInterceptor) checkKey(field, value string) error {
	if value == "" {
		return v.reject("empty_key", "%s must not be empty", field)
	}
	if v.limits.MaxKeyLen > 0 && len(value) > v.limits.MaxKeyLen {
		return v.reject("key_too_long", "%s length %d exceeds max %d", field, len(value), v.limits.MaxKeyLen)
	}
	return nil
}

func (v *ValidationInterceptor) checkBatch(n int) error {
	if n == 0 {
		return v.reject("empty_batch", "request batch must not be empty")
	}
	if v.limits.MaxBatchSize > 0 && n > v.limits.MaxBatchSize {
		return v.reject("batch_too_large", "batch size %d exceeds max %d", n, v.limits.MaxBatchSize)
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
	lim        *rate.Limiter
	rejectHook func()
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

// WithRejectHook registers a callback invoked once per rejected RPC
// before codes.ResourceExhausted is returned. Used by provider/metrics
// to bump lantern_rate_limit_rejected_total. A nil hook disables the
// callback; safe to call exactly once during wiring.
func (r *RateLimitInterceptor) WithRejectHook(f func()) *RateLimitInterceptor {
	r.rejectHook = f
	return r
}

// ---------------------------------------------------------------------------
// TLS
// ---------------------------------------------------------------------------

// loadClientCAPool reads the PEM-encoded CA bundle at path into a
// fresh *x509.CertPool. Returns an error when the file is missing /
// unreadable or contains zero parseable certificates. Consumed by
// loadTLSConfig (lantern_listener.go) when LANTERN_TLS_CLIENT_CA_FILE
// is set; kept here so the TLS knobs live alongside the TLSConfig type.
func loadClientCAPool(path string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("client CA file contained no usable certificates")
	}
	return pool, nil
}
