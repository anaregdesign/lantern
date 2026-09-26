// Package client: retry.go implements the opt-in retry policy (#849).
//
// WithRetry applies context-aware, bounded exponential backoff with full
// jitter only where an ambiguous response cannot change the result or
// repeat an unsafe mutation. ErrUnavailable does not prove the server
// rejected the request.
//
// Eligibility is enforced in code, not docs (see requestRetryable):
//
//	reads (Get*/Scan*/Count*/Search*/Illuminate/status)  retryable
//	unconditional PutVertex(es)/PutEdge(s)                retryable
//	plain exact/prefix Deletes, conditional Put, Add      never: original results or
//	                                                     intervening Deletes make replay unsafe
//	receipt-bearing mutations                            never here; the dedicated path verifies
//	                                                     endpoint continuity before each attempt
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
// be (re)sent. ContribIDs only deduplicate while the contribution is live:
// an intervening Delete or expiration makes plain Add unsafe to replay.
// Unknown request types are NOT retryable — fail closed; the paired matrix
// test forces a classification for every public RPC method.
func requestRetryable(req any) bool {
	switch r := req.(type) {
	case *pb.PutVertexRequest:
		return r.GetReceiptContext() == nil && !r.GetIfAbsent()
	case *pb.PutVerticesRequest:
		return r.GetReceiptContext() == nil && !r.GetIfAbsent()
	case *pb.AddEdgeRequest, *pb.AddEdgesRequest,
		*pb.DeleteVertexRequest, *pb.DeleteVerticesRequest,
		*pb.DeleteEdgeRequest, *pb.DeleteEdgesRequest,
		*pb.DeleteVerticesByPrefixRequest, *pb.DeleteEdgesByPrefixRequest:
		return false
	case *pb.GetVertexRequest, *pb.GetVerticesRequest,
		*pb.GetEdgeRequest, *pb.GetEdgesRequest,
		*pb.PutEdgeRequest, *pb.PutEdgesRequest,
		*pb.ScanVerticesRequest, *pb.ScanVertexKeysRequest,
		*pb.ScanEdgesRequest, *pb.CountVerticesByPrefixRequest,
		*pb.SearchVerticesRequest, *pb.IlluminateRequest,
		*pb.GetServerStatusRequest, *pb.GetReplicationStatusRequest,
		*pb.GetReceiptCapabilityRequest, *pb.GetReceiptStatusRequest,
		*pb.GetReceiptStatusesRequest:
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
	// retryNever: never replayed (unsafe writes, streaming, or unclassified).
	retryNever methodRetryClass = iota
	// retryAlways: eligible for replay, including endpoint-bound receipt
	// mutations through their dedicated continuity-aware path.
	retryAlways
)

// methodRetryClasses classifies every public RPC-shaped method on *Lantern
// (and therefore every Failover wrapper). Adding an RPC method without a
// row here fails TestRetryEligibilityMatrix_CoversEveryRPC.
var methodRetryClasses = map[string]methodRetryClass{
	"GetVertex":                      retryAlways,
	"GetVertices":                    retryAlways,
	"GetEdge":                        retryAlways,
	"GetEdges":                       retryAlways,
	"PutVertex":                      retryAlways,
	"PutVertexAt":                    retryAlways,
	"PutVertices":                    retryAlways,
	"PutVertexIfAbsent":              retryNever,
	"PutVertexIfAbsentAt":            retryNever,
	"PutVerticesIfAbsent":            retryNever,
	"PutEdge":                        retryAlways,
	"PutEdgeAt":                      retryAlways,
	"PutEdges":                       retryAlways,
	"DeleteVertex":                   retryNever,
	"DeleteVertices":                 retryNever,
	"DeleteEdge":                     retryNever,
	"DeleteEdges":                    retryNever,
	"DeleteVerticesByPrefix":         retryNever,
	"DeleteEdgesByPrefix":            retryNever,
	"ScanVertices":                   retryAlways,
	"ScanVerticesAll":                retryAlways,
	"ScanVertexKeys":                 retryAlways,
	"ScanVertexKeysAll":              retryAlways,
	"ScanEdges":                      retryAlways,
	"ScanEdgesAll":                   retryAlways,
	"CountVerticesByPrefix":          retryAlways,
	"SearchVertices":                 retryAlways,
	"SearchVerticesPage":             retryAlways,
	"Illuminate":                     retryAlways,
	"GetServerStatus":                retryAlways,
	"GetReplicationStatus":           retryAlways,
	"GetReceiptCapability":           retryAlways,
	"GetReceiptStatus":               retryAlways,
	"GetReceiptStatuses":             retryAlways,
	"PutVertexWithReceipt":           retryAlways,
	"PutVertexAtWithReceipt":         retryAlways,
	"PutVerticesWithReceipt":         retryAlways,
	"PutVertexIfAbsentWithReceipt":   retryAlways,
	"PutVertexIfAbsentAtWithReceipt": retryAlways,
	"PutVerticesIfAbsentWithReceipt": retryAlways,
	"DeleteVertexWithReceipt":        retryAlways,
	"DeleteVerticesWithReceipt":      retryAlways,
	"DeleteEdgeWithReceipt":          retryAlways,
	"DeleteEdgesWithReceipt":         retryAlways,
	"AddEdgeWithReceipt":             retryAlways,
	"AddEdgeAtWithReceipt":           retryAlways,
	"AddEdgesWithReceipt":            retryAlways,
	"Ping":                           retryAlways,
	"AddEdge":                        retryNever,
	"AddEdgeAt":                      retryNever,
	"AddEdges":                       retryNever,
	"AddDecayingEdge":                retryNever, // fans out into an AddEdges batch

	"Backup":               retryNever, // whole-graph stream dump — excluded in v1
	"Restore":              retryNever, // stream restore — excluded in v1
	"Subscribe":            retryNever, // server-streaming replication feed
	"BootstrapIdentity":    retryNever, // recovery stream must not retry invisibly
	"SubscribeIdentity":    retryNever, // caller owns its durable vector cursor
	"NewIncrementalSearch": retryNever, // session constructor; per-query retries ride unary
	"SearchVerticesIter":   retryNever, // local iterator; each page owns its unary retry
}

// retryableMethod is the method-name counterpart to requestRetryable,
// consumed by Failover. Unknown methods fail closed (retryNever is the
// zero value). Receipt-bearing methods use this only on a pinned endpoint.
func retryableMethod(method string) bool {
	return methodRetryClasses[method] == retryAlways
}
