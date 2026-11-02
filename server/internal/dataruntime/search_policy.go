package dataruntime

import (
	"context"
	"errors"
	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/query"
)

type SearchRequestPolicy = query.RequestPolicy

func SearchRequestPolicyFromLimits(limits SearchLimits) SearchRequestPolicy {
	return query.RequestPolicyFromLimits(limits)
}

// SearchPolicyVersion binds a managed generation to the complete resolved
// compatibility contract, including excluded local index/session settings.
type SearchPolicyVersion struct {
	Generation  uint64
	Fingerprint string
}

type SearchPolicySnapshot struct {
	Version SearchPolicyVersion
	Policy  SearchRequestPolicy
}

// SearchPolicyAuthority reads the certified desired version from the request's
// current private admission. It must fail closed when that admission is absent,
// stale or unavailable. It is independent of query limits and ACL compilation.
// Nil is reserved for explicitly environment-owned operation.
type SearchPolicyAuthority interface {
	DesiredSearchPolicy(context.Context) (SearchPolicyVersion, error)
}

type searchPolicyEngine struct {
	snapshot SearchPolicySnapshot
	engine   *query.Engine
}

type searchPolicyState struct {
	desired   SearchPolicySnapshot
	effective *searchPolicyEngine
}

// SearchPolicyStatus reports the installed engine separately from its staged
// desired settings. Committing a policy alone never reports it applied.
type SearchPolicyStatus struct {
	Desired        SearchPolicySnapshot
	Effective      SearchPolicySnapshot
	PendingRestart bool
}

// SearchPolicyOwner replaces complete query engines while retaining the same
// graph/index Reader. Old requests pin their immutable engine; no in-place
// settings, semaphore or cursor-store mutation occurs.
type SearchPolicyOwner struct {
	baseline  query.Limits
	authority SearchPolicyAuthority
	state     atomic.Pointer[searchPolicyState]
}

// ResolveSearchPolicy is read-only candidate preflight against the exact local
// baseline. Use its full fingerprint in the native fixed-cohort proof.
func ResolveSearchPolicy(baseline SearchLimits, generation uint64, policy SearchRequestPolicy) (SearchPolicySnapshot, error) {
	_, fingerprint, err := policy.Resolve(baseline)
	if err != nil {
		return SearchPolicySnapshot{}, err
	}
	return SearchPolicySnapshot{Version: SearchPolicyVersion{generation, fingerprint}, Policy: policy}, nil
}

func NewSearchPolicyOwner(baseline SearchLimits, initial SearchPolicySnapshot, authority SearchPolicyAuthority) (*SearchPolicyOwner, error) {
	resolved, fingerprint, err := initial.Policy.Resolve(baseline)
	if err != nil {
		return nil, err
	}
	if initial.Version.Fingerprint != fingerprint {
		return nil, errors.New("search policy local compatibility fingerprint mismatch")
	}
	owner := &SearchPolicyOwner{baseline: baseline, authority: authority}
	owner.state.Store(&searchPolicyState{desired: initial, effective: &searchPolicyEngine{initial, query.New(resolved)}})
	return owner, nil
}

func (o *SearchPolicyOwner) Status() SearchPolicyStatus {
	state := o.state.Load()
	return SearchPolicyStatus{state.desired, state.effective.snapshot, state.desired != state.effective.snapshot}
}

func (o *SearchPolicyOwner) SearchConfigFingerprint() string {
	return o.state.Load().effective.engine.ConfigFingerprint()
}

// StageDesired records a verified committed policy without applying it. New
// Search RPCs remain unavailable until an explicit engine restart is complete.
func (o *SearchPolicyOwner) StageDesired(next SearchPolicySnapshot) error {
	_, fingerprint, err := next.Policy.Resolve(o.baseline)
	if err != nil {
		return err
	}
	if next.Version.Fingerprint != fingerprint {
		return errors.New("search policy local compatibility fingerprint mismatch")
	}
	for {
		previous := o.state.Load()
		if next.Version.Generation < previous.desired.Version.Generation || next.Version.Generation == previous.desired.Version.Generation && next != previous.desired {
			return errors.New("search desired generation conflict")
		}
		if next == previous.desired || o.state.CompareAndSwap(previous, &searchPolicyState{next, previous.effective}) {
			return nil
		}
	}
}

// Restart replaces only the versioned query engine and its bounded cursor
// store. The exact staged snapshot is the CAS token; repeated activation of an
// already effective version preserves its cursor store. It never rebuilds or
// discards graph/index data and does not claim peer worker activation.
func (o *SearchPolicyOwner) Restart(expected SearchPolicySnapshot) error {
	previous := o.state.Load()
	if previous.desired != expected {
		return errors.New("search restart desired generation conflict")
	}
	if previous.effective.snapshot == expected {
		return nil
	}
	resolved, fingerprint, err := expected.Policy.Resolve(o.baseline)
	if err != nil {
		return err
	}
	if fingerprint != expected.Version.Fingerprint {
		return errors.New("search policy local compatibility fingerprint mismatch")
	}
	next := &searchPolicyState{expected, &searchPolicyEngine{expected, query.New(resolved)}}
	if !o.state.CompareAndSwap(previous, next) {
		current := o.state.Load()
		if current.desired == expected && current.effective.snapshot == expected {
			return nil
		}
		return errors.New("search restart desired generation conflict")
	}
	return nil
}

func (o *SearchPolicyOwner) check(ctx context.Context, effective *searchPolicyEngine) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	state := o.state.Load()
	if state.effective != effective || state.desired != effective.snapshot {
		return connect.NewError(connect.CodeUnavailable, errors.New("search policy requires engine restart"))
	}
	if o.authority != nil {
		desired, err := o.authority.DesiredSearchPolicy(ctx)
		if err != nil {
			return connect.NewError(connect.CodeUnavailable, errors.New("search policy authority unavailable"))
		}
		if desired != effective.snapshot.Version {
			return connect.NewError(connect.CodeUnavailable, errors.New("search effective policy does not match committed authority"))
		}
	}
	if o.state.Load() != state {
		return connect.NewError(connect.CodeUnavailable, errors.New("search policy changed during admission"))
	}
	return nil
}

type searchPolicyLease struct {
	owner     *SearchPolicyOwner
	effective *searchPolicyEngine
}

type searchPolicyContextKey struct{}

func (s *Executor) captureSearchPolicy(ctx context.Context) (context.Context, error) {
	if s.searchPolicies == nil {
		return ctx, nil
	}
	if lease, ok := ctx.Value(searchPolicyContextKey{}).(searchPolicyLease); ok && lease.owner == s.searchPolicies {
		return ctx, lease.owner.check(ctx, lease.effective)
	}
	lease := searchPolicyLease{s.searchPolicies, s.searchPolicies.state.Load().effective}
	if err := lease.owner.check(ctx, lease.effective); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, searchPolicyContextKey{}, lease), nil
}

func checkSearchPolicyOutput(ctx context.Context) error {
	if lease, ok := ctx.Value(searchPolicyContextKey{}).(searchPolicyLease); ok {
		return lease.owner.check(ctx, lease.effective)
	}
	return nil
}

// WithSearchPolicyOwner is startup composition. The owner must be recovered
// and installed before certification; ordinary graph writes cannot change it.
func (s *Executor) WithSearchPolicyOwner(owner *SearchPolicyOwner) *Executor {
	s.searchPolicies = owner
	return s
}

func (s *Executor) effectiveSearchEngine() *query.Engine {
	if s.searchPolicies != nil {
		return s.searchPolicies.state.Load().effective.engine
	}
	return s.search
}

// SearchPolicyBaseline returns the actual normalized engine inputs for
// read-only candidate resolution. It grants no public management capability.
func (s *Executor) SearchPolicyBaseline() SearchLimits {
	return s.effectiveSearchEngine().Limits()
}
