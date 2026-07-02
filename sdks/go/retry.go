// Package client: retry.go implements the opt-in retry policy (#849).
//
// The SDK long had every prerequisite for safe retries — the
// ErrUnavailable sentinel, WithIdempotentAdds ContribID stamping, failover
// rotation — and stopped one step short of performing them, so every
// consumer hand-rolled the same backoff loop (or, in practice, didn't).
// WithRetry closes that gap: exponential backoff with full jitter,
// context-aware, bounded attempts, applied ONLY to RPCs that are
// idempotent under the client's configuration.
//
// Eligibility is enforced in code, not docs (see requestRetryable):
//
//	reads (Get*/Scan*/Count*/Search*/Illuminate/status)  retryable
//	PutVertex(es)/PutEdge(s)/Delete*                     retryable (idempotent semantics)
//	AddEdge/AddEdges                                     retryable ONLY when every edge in the
//	                                                     request carries a ContribID (WithIdempotentAdds
//	                                                     stamps them; without one a retry double-counts)
//	streaming / io (Subscribe/Backup/Restore/…)          never (v1)
//	anything unclassified                                never (fail closed)
//
// Never retried regardless of policy: deterministic outcomes
// (NotFound/InvalidArgument/FailedPrecondition) and DeadlineExceeded (the
// budget is already spent). Default retryable code: Unavailable.
// ResourceExhausted is opt-in via RetryableCodes so it composes with the
// server-side capacity cap (#848) and rate limiter when the caller wants
// retry-after-decay semantics.
package client

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// RetryPolicy configures WithRetry. The zero value is normalised to
// MaxAttempts=3, BaseDelay=100ms, MaxDelay=2s, RetryableCodes={Unavailable}.
type RetryPolicy struct {
	// MaxAttempts is the total number of tries including the first.
	// Values < 1 normalise to 3. Under NewLanternFailover the attempts
	// double as the cross-replica budget: an Unavailable attempt advances
	// the sticky endpoint, so the next attempt hits the sibling.
	MaxAttempts int
	// BaseDelay seeds the exponential backoff (default 100ms). The delay
	// before attempt k (k >= 2) is uniformly random in
	// (0, min(MaxDelay, BaseDelay·2^(k-2))] — "full jitter".
	BaseDelay time.Duration
	// MaxDelay caps a single backoff sleep (default 2s).
	MaxDelay time.Duration
	// RetryableCodes lists the connect codes worth another attempt.
	// Empty defaults to {CodeUnavailable}. Add CodeResourceExhausted to
	// retry through the #848 capacity cap / rate limiter.
	RetryableCodes []connect.Code

	// Test seams: injected sleep and randomness. Nil uses real time and
	// math/rand. Unexported — production callers cannot (and must not)
	// touch them.
	sleepFn func(context.Context, time.Duration) error
	randFn  func() float64
}

// normalized returns a copy with defaults applied.
func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 3
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 100 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 2 * time.Second
	}
	if len(p.RetryableCodes) == 0 {
		p.RetryableCodes = []connect.Code{connect.CodeUnavailable}
	}
	if p.sleepFn == nil {
		p.sleepFn = ctxSleep
	}
	if p.randFn == nil {
		p.randFn = rand.Float64
	}
	return p
}

// retryableErr reports whether err is worth another attempt under the
// policy. nil is never retryable (success); DeadlineExceeded/Canceled are
// excluded structurally because the caller's ctx is already dead.
func (p RetryPolicy) retryableErr(err error) bool {
	if err == nil {
		return false
	}
	code := connect.CodeOf(err)
	if code == connect.CodeDeadlineExceeded || code == connect.CodeCanceled {
		return false
	}
	for _, c := range p.RetryableCodes {
		if code == c {
			return true
		}
	}
	return false
}

// delay computes the full-jitter backoff before attempt number `attempt`
// (0-based count of completed attempts): rand(0, min(MaxDelay, Base·2^n)].
func (p RetryPolicy) delay(attempt int) time.Duration {
	ceil := p.BaseDelay << attempt
	if ceil <= 0 || ceil > p.MaxDelay { // <<-overflow guards land on MaxDelay
		ceil = p.MaxDelay
	}
	return time.Duration(p.randFn() * float64(ceil))
}

// run drives attempt() under the policy: first try immediately, then up to
// MaxAttempts-1 retries with jittered backoff. A non-retryable error (or
// success) returns immediately. Backoff sleeps honour ctx; cancellation
// mid-backoff aborts with the last attempt's error joined with ctx.Err().
func (p RetryPolicy) run(ctx context.Context, attempt func() error) error {
	var lastErr error
	for i := 0; i < p.MaxAttempts; i++ {
		if i > 0 {
			if err := p.sleepFn(ctx, p.delay(i-1)); err != nil {
				return errors.Join(lastErr, err)
			}
		}
		lastErr = attempt()
		if !p.retryableErr(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

// ctxSleep sleeps for d or until ctx is done, whichever comes first.
func ctxSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// requestRetryable is the code-enforced eligibility matrix at the wire
// boundary (#849): it classifies by the REQUEST message that is about to
// be (re)sent, which uniformly covers both WithIdempotentAdds (ContribIDs
// are stamped into the request before the first attempt, so every retry
// re-sends the same keys) and explicitly-supplied ContribIDs. Unknown
// request types are NOT retryable — fail closed; the paired matrix test
// forces a classification for every public RPC method.
func requestRetryable(req any) bool {
	switch r := req.(type) {
	case *pb.AddEdgeRequest:
		return len(r.GetContribId()) > 0
	case *pb.AddEdgesRequest:
		if len(r.GetContribIds()) != len(r.GetEdges()) {
			return false
		}
		for _, id := range r.GetContribIds() {
			if len(id) == 0 {
				return false
			}
		}
		return len(r.GetEdges()) > 0
	case *pb.GetVertexRequest, *pb.GetVerticesRequest,
		*pb.GetEdgeRequest, *pb.GetEdgesRequest,
		*pb.PutVertexRequest, *pb.PutVerticesRequest,
		*pb.PutEdgeRequest, *pb.PutEdgesRequest,
		*pb.DeleteVertexRequest, *pb.DeleteVerticesRequest,
		*pb.DeleteEdgeRequest, *pb.DeleteEdgesRequest,
		*pb.DeleteVerticesByPrefixRequest,
		*pb.ScanVerticesRequest, *pb.ScanVertexKeysRequest,
		*pb.ScanEdgesRequest, *pb.CountVerticesByPrefixRequest,
		*pb.SearchVerticesRequest, *pb.IlluminateRequest,
		*pb.GetServerStatusRequest, *pb.GetReplicationStatusRequest:
		return true
	}
	return false
}

// methodRetryClass is the SDK-method-level eligibility matrix. It exists
// for two consumers: the Failover wrappers (which dispatch per method, not
// per wire request) and the paired reflection test that walks EVERY public
// *Lantern RPC method and fails the build-time contract when a new method
// lands without a classification — the forced-decision rule.
type methodRetryClass int

const (
	// retryNever: never retried (streaming, or unclassified).
	retryNever methodRetryClass = iota
	// retryAlways: idempotent by semantics.
	retryAlways
	// retryIfIdempotentAdds: additive writes — retried only when the
	// request provably carries per-edge ContribIDs.
	retryIfIdempotentAdds
)

// methodRetryClasses classifies every public RPC-shaped method on *Lantern
// (and therefore every Failover wrapper). Adding an RPC method without a
// row here fails TestRetryEligibilityMatrix_CoversEveryRPC.
var methodRetryClasses = map[string]methodRetryClass{
	"GetVertex":              retryAlways,
	"GetVertices":            retryAlways,
	"GetEdge":                retryAlways,
	"GetEdges":               retryAlways,
	"PutVertex":              retryAlways,
	"PutVertexAt":            retryAlways,
	"PutVertices":            retryAlways,
	"PutEdge":                retryAlways,
	"PutEdgeAt":              retryAlways,
	"PutEdges":               retryAlways,
	"DeleteVertex":           retryAlways,
	"DeleteVertices":         retryAlways,
	"DeleteEdge":             retryAlways,
	"DeleteEdges":            retryAlways,
	"DeleteVerticesByPrefix": retryAlways,
	"ScanVertices":           retryAlways,
	"ScanVerticesAll":        retryAlways,
	"ScanVertexKeys":         retryAlways,
	"ScanVertexKeysAll":      retryAlways,
	"ScanEdges":              retryAlways,
	"ScanEdgesAll":           retryAlways,
	"CountVerticesByPrefix":  retryAlways,
	"SearchVertices":         retryAlways,
	"Illuminate":             retryAlways,
	"GetServerStatus":        retryAlways,
	"GetReplicationStatus":   retryAlways,
	"Ping":                   retryAlways,
	"AddEdge":                retryIfIdempotentAdds,
	"AddEdgeAt":              retryIfIdempotentAdds,
	"AddEdges":               retryIfIdempotentAdds,
	"Backup":                 retryNever, // whole-graph stream dump — excluded in v1
	"Restore":                retryNever, // stream restore — excluded in v1
	"Subscribe":              retryNever, // server-streaming replication feed
	"NewIncrementalSearch":   retryNever, // session constructor; per-query retries ride unary
}

// retryableMethod is the method-name counterpart to requestRetryable,
// consumed by the Failover wrappers — which dispatch per method, not per
// wire request, so they cannot inspect the ContribIDs the way unary does.
// It reads the code-enforced methodRetryClasses matrix and folds in the
// caller's idempotent-adds setting for the additive write surface. Unknown
// methods fail closed (retryNever is the zero value), so a mis-typed call
// site silently disables retry rather than risking an unsafe one.
func retryableMethod(method string, idempotentAdds bool) bool {
	switch methodRetryClasses[method] {
	case retryAlways:
		return true
	case retryIfIdempotentAdds:
		return idempotentAdds
	default:
		return false
	}
}
