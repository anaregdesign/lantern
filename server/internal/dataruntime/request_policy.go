package dataruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// RequestValidationLimits bounds the dimensions checked before data execution.
// Zero disables a dimension; negative legacy bootstrap values are normalized to
// zero before adoption, never accepted in a managed policy.
type RequestValidationLimits struct {
	MaxKeyLen         int
	MaxBatchSize      int
	IlluminateMaxStep int
	IlluminateMaxK    int
}

// RequestPolicy contains exactly the twelve request, scan and traversal knobs.
// It contains no authorization state. Each public request retains one value
// snapshot while its independent admission checks remain current.
type RequestPolicy struct {
	Validation       RequestValidationLimits
	Scan             ScanLimits
	Traversal        TraversalLimits
	TraversalTimeout time.Duration
}

// Validate rejects an invalid managed value as a whole, without clamping it.
func (p RequestPolicy) Validate() error {
	if p.Validation.MaxKeyLen < 0 || p.Validation.MaxBatchSize < 0 || p.Validation.IlluminateMaxStep < 0 || p.Validation.IlluminateMaxK < 0 {
		return errors.New("request validation limits must be nonnegative; zero disables a limit")
	}
	if p.Scan.ScanDefaultLimit == 0 || p.Scan.ScanMaxLimit == 0 || p.Scan.ScanDefaultLimit > p.Scan.ScanMaxLimit {
		return errors.New("scan limits must be positive with default at most maximum")
	}
	if p.Scan.DeleteByPrefixDefaultLimit == 0 || p.Scan.DeleteByPrefixMaxLimit == 0 || p.Scan.DeleteByPrefixDefaultLimit > p.Scan.DeleteByPrefixMaxLimit {
		return errors.New("delete-by-prefix limits must be positive with default at most maximum")
	}
	if p.Traversal.WorkBudget.MaxPushes <= 0 || p.Traversal.WorkBudget.MaxTouchedEdges <= 0 || p.Traversal.MaxResults <= 0 {
		return errors.New("traversal work and result limits must be positive")
	}
	if p.TraversalTimeout < 0 || p.TraversalTimeout%time.Millisecond != 0 {
		return errors.New("traversal timeout must be a nonnegative whole millisecond duration")
	}
	return nil
}

// RequestPolicySnapshot identifies the actual, locally installed generation.
// Publishing desired authority is a separate operation owned by the control
// plane; an older effective generation must be reported honestly until applied.
type RequestPolicySnapshot struct {
	Generation uint64
	Policy     RequestPolicy
}

// RequestPolicySource supplies an immutable value, safe for concurrent reads.
type RequestPolicySource interface {
	RequestPolicySnapshot() RequestPolicySnapshot
}

// RequestPolicyOwner publishes an entire effective group atomically. It adds no
// persistence or authority protocol; only the certified management adapter may
// install committed generations in production.
type RequestPolicyOwner struct {
	current atomic.Pointer[RequestPolicySnapshot]
}

func NewRequestPolicyOwner(initial RequestPolicySnapshot) (*RequestPolicyOwner, error) {
	if err := initial.Policy.Validate(); err != nil {
		return nil, err
	}
	o := &RequestPolicyOwner{}
	o.current.Store(&initial)
	return o, nil
}

// NewBootstrapRequestPolicyOwner retains actual legacy startup values at
// generation zero, including values that cannot be adopted as managed policy.
// It is trusted pre-serving composition only. Every later Apply still validates
// the entire canonical policy; no defaults or clamping repair startup input.
func NewBootstrapRequestPolicyOwner(actual RequestPolicy) *RequestPolicyOwner {
	o := &RequestPolicyOwner{}
	o.current.Store(&RequestPolicySnapshot{Policy: actual})
	return o
}

func (o *RequestPolicyOwner) RequestPolicySnapshot() RequestPolicySnapshot {
	return *o.current.Load()
}

// Apply installs one validated effective generation, rejecting stale or
// conflicting callbacks. Replaying the exact installed generation is harmless.
func (o *RequestPolicyOwner) Apply(next RequestPolicySnapshot) error {
	if err := next.Policy.Validate(); err != nil {
		return err
	}
	for {
		previous := o.current.Load()
		if next.Generation < previous.Generation || next.Generation == previous.Generation && next.Policy != previous.Policy {
			return errors.New("request policy effective generation conflict")
		}
		if next == *previous || o.current.CompareAndSwap(previous, &next) {
			return nil
		}
	}
}

type requestPolicyContextKey struct{}

// CaptureRequestPolicy captures at most once, before transport validation.
// This context value carries only operational limits, never authentication or
// permission to skip the native admission boundary.
func CaptureRequestPolicy(ctx context.Context, source RequestPolicySource) context.Context {
	if _, ok := RequestPolicyFromContext(ctx); ok || source == nil {
		return ctx
	}
	return context.WithValue(ctx, requestPolicyContextKey{}, source.RequestPolicySnapshot())
}

func RequestPolicyFromContext(ctx context.Context) (RequestPolicySnapshot, bool) {
	p, ok := ctx.Value(requestPolicyContextKey{}).(RequestPolicySnapshot)
	return p, ok
}

// WithRequestPolicySource binds the same owner used by transport validation.
// Like other With methods, it is a composition-time operation before serving.
func (s *Executor) WithRequestPolicySource(source RequestPolicySource) *Executor {
	s.requestPolicies = source
	return s
}

// BootstrapRequestPolicy reads the actual legacy executor configuration before
// an owner is installed. Validation comes from the actual transport validator.
func (s *Executor) BootstrapRequestPolicy(validation RequestValidationLimits) (RequestPolicy, error) {
	if s == nil || s.requestPolicies != nil {
		return RequestPolicy{}, errors.New("request policy bootstrap ownership unavailable")
	}
	actual := s.requestPolicy(context.Background())
	actual.Validation = validation
	return actual, nil
}

func (s *Executor) UsesRequestPolicyOwner(owner *RequestPolicyOwner) bool {
	return s != nil && owner != nil && s.requestPolicies == owner
}

func (s *Executor) captureRequestPolicy(ctx context.Context) context.Context {
	return CaptureRequestPolicy(ctx, s.requestPolicies)
}

func (s *Executor) requestPolicy(ctx context.Context) RequestPolicy {
	if snapshot, ok := RequestPolicyFromContext(ctx); ok {
		return snapshot.Policy
	}
	return RequestPolicy{
		Scan:             s.scan,
		Traversal:        TraversalLimits{WorkBudget: s.traversalWorkBudget, MaxResults: s.traversalMaxResults},
		TraversalTimeout: s.traversalTimeout,
	}
}
