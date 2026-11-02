package dataruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
)

func newSearchPolicyFixture(t *testing.T, authority SearchPolicyAuthority) (*SearchPolicyOwner, SearchPolicySnapshot) {
	t.Helper()
	baseline := query.DefaultLimits()
	baseline.Enabled, baseline.DefaultMinShould = true, 1
	initial, err := ResolveSearchPolicy(baseline, 5, query.RequestPolicyFromLimits(baseline))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewSearchPolicyOwner(baseline, initial, authority)
	if err != nil {
		t.Fatal(err)
	}
	return owner, initial
}

func nextSearchPolicy(t *testing.T, owner *SearchPolicyOwner, initial SearchPolicySnapshot) SearchPolicySnapshot {
	t.Helper()
	p := initial.Policy
	p.DefaultLimit, p.MaxLimit = 1, 2
	p.WorkBudget.MaxPostingVisits = 7
	next, err := ResolveSearchPolicy(owner.baseline, initial.Version.Generation+1, p)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestSearchPolicyOwnerStagesAndRestartsExactVersion(t *testing.T) {
	owner, initial := newSearchPolicyFixture(t, nil)
	old := owner.state.Load().effective
	next := nextSearchPolicy(t, owner, initial)
	if err := owner.StageDesired(next); err != nil {
		t.Fatal(err)
	}
	status := owner.Status()
	if status.Desired != next || status.Effective != initial || !status.PendingRestart || owner.SearchConfigFingerprint() != initial.Version.Fingerprint {
		t.Fatalf("staged status = %+v", status)
	}
	if err := owner.check(t.Context(), old); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("pending engine admitted: %v", err)
	}
	if err := owner.Restart(initial); err == nil {
		t.Fatal("stale restart accepted")
	}
	conflict := next
	conflict.Version.Fingerprint = "mismatched actual baseline"
	if err := owner.StageDesired(conflict); err == nil {
		t.Fatal("fingerprint mismatch accepted")
	}
	if err := owner.StageDesired(initial); err == nil {
		t.Fatal("stale desired accepted")
	}
	if err := owner.Restart(next); err != nil {
		t.Fatal(err)
	}
	installed := owner.state.Load().effective
	if installed == old || installed.engine == old.engine || installed.engine.Limits().MaxLimit != 2 || owner.Status().PendingRestart {
		t.Fatal("engine not replaced completely")
	}
	if old.engine.Limits().MaxLimit != initial.Policy.MaxLimit {
		t.Fatal("in-flight old engine mutated")
	}
	if err := owner.Restart(next); err != nil || owner.state.Load().effective != installed {
		t.Fatal("restart replay rotated active cursor store")
	}
	if err := owner.check(t.Context(), old); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("retired engine output admitted")
	}
	conflictingPolicy := next.Policy
	conflictingPolicy.MaxQueryBytes--
	sameGeneration, err := ResolveSearchPolicy(owner.baseline, next.Version.Generation, conflictingPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.StageDesired(sameGeneration); err == nil {
		t.Fatal("conflicting same-generation policy accepted")
	}
}

func TestSearchPolicyConcurrentRestartRecoveryAndRollback(t *testing.T) {
	owner, initial := newSearchPolicyFixture(t, nil)
	next := nextSearchPolicy(t, owner, initial)
	if err := owner.StageDesired(next); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			if err := owner.Restart(next); err != nil {
				t.Errorf("same-command activation recovery: %v", err)
			}
		})
	}
	wait.Wait()
	installed := owner.state.Load().effective
	if installed.snapshot != next {
		t.Fatal("concurrent activation installed another generation")
	}
	if err := owner.Restart(next); err != nil || owner.state.Load().effective != installed {
		t.Fatal("recovery command rotated cursor store")
	}
	rollback, err := ResolveSearchPolicy(owner.baseline, next.Version.Generation+1, initial.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Version.Fingerprint != initial.Version.Fingerprint || rollback.Version.Generation == initial.Version.Generation {
		t.Fatal("rollback lost value/generation distinction")
	}
	if err := owner.StageDesired(rollback); err != nil {
		t.Fatal(err)
	}
	if err := owner.Restart(rollback); err != nil {
		t.Fatal(err)
	}
	if owner.Status().Effective != rollback || owner.Status().PendingRestart {
		t.Fatal("rollback not installed as new generation")
	}
	if err := owner.check(t.Context(), installed); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("previous engine survived reviewed rollback")
	}
}

type searchPolicyAuthorityFixture struct {
	mu      sync.Mutex
	version SearchPolicyVersion
	err     error
}

func (a *searchPolicyAuthorityFixture) DesiredSearchPolicy(context.Context) (SearchPolicyVersion, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.version, a.err
}

func TestSearchPolicyFreshAuthorityClosesCallbackGapAndFailsClosed(t *testing.T) {
	a := &searchPolicyAuthorityFixture{}
	owner, initial := newSearchPolicyFixture(t, a)
	a.version = initial.Version
	if err := owner.check(t.Context(), owner.state.Load().effective); err != nil {
		t.Fatal(err)
	}
	next := nextSearchPolicy(t, owner, initial)
	// Authority committed before the asynchronous owner observer runs.
	a.version = next.Version
	if err := owner.check(t.Context(), owner.state.Load().effective); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("stale effective admitted during callback gap: %v", err)
	}
	if owner.Status().Effective != initial {
		t.Fatal("desired commit falsely reported effective")
	}
	a.version = initial.Version
	a.err = errors.New("quorum unavailable")
	if err := owner.check(t.Context(), owner.state.Load().effective); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("authority failure fell back to local state: %v", err)
	}
}

func TestSearchPolicyInFlightPinsEngineAndRefusesTransitionOutput(t *testing.T) {
	owner, initial := newSearchPolicyFixture(t, nil)
	backend := newFakeBackend()
	entered, release := make(chan struct{}), make(chan struct{})
	backend.searchContextFn = func(ctx context.Context) ([]search.Result[string], search.Stats, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, search.Stats{}, ctx.Err()
		}
		return []search.Result[string]{{ID: "a", Score: 1}}, search.Stats{}, nil
	}
	svc := NewExecutor(backend).WithSearchPolicyOwner(owner)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := svc.Public().SearchVertices(ctx, &pb.SearchVerticesRequest{Query: "needle"})
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	next := nextSearchPolicy(t, owner, initial)
	if err := owner.StageDesired(next); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Public().SearchVertices(ctx, &pb.SearchVerticesRequest{Query: "needle"}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("direct pending Search admitted: %v", err)
	}
	if err := owner.Restart(next); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("old generation output accepted: %v", err)
	}
	if backend.lastSearchBudget != initial.Policy.WorkBudget {
		t.Fatalf("in-flight budget mutated: %+v", backend.lastSearchBudget)
	}
	if svc.SearchConfigFingerprint() != next.Version.Fingerprint || svc.SearchPolicyBaseline().WorkBudget != next.Policy.WorkBudget {
		t.Fatal("effective status/fingerprint is stale")
	}
	if _, err := svc.GetServerStatus(ctx, &pb.GetServerStatusRequest{}); err != nil {
		t.Fatalf("status unavailable during transition: %v", err)
	}
}

func TestSearchPolicyOwnerRejectsCapabilityOverflowAndReportsExactMaximum(t *testing.T) {
	const maxCapability = int64(1<<32 - 1)
	owner, initial := newSearchPolicyFixture(t, nil)
	for _, change := range []func(*SearchRequestPolicy){
		func(p *SearchRequestPolicy) { p.Timeout = time.Duration(maxCapability+1) * time.Millisecond },
		func(p *SearchRequestPolicy) { p.MaxQueryBytes = int(maxCapability + 1) },
		func(p *SearchRequestPolicy) {
			p.MaxQueryBytes = int(maxCapability)
			p.WorkBudget.MaxQueryTerms = maxCapability + 1
		},
	} {
		invalid := initial
		invalid.Version.Generation++
		change(&invalid.Policy)
		if err := owner.StageDesired(invalid); err == nil || !strings.Contains(err.Error(), "uint32") {
			t.Fatalf("invalid candidate not rejected by capability validation: %v", err)
		}
		if _, err := NewSearchPolicyOwner(owner.baseline, invalid, nil); err == nil || !strings.Contains(err.Error(), "uint32") {
			t.Fatalf("invalid restore not rejected by capability validation: %v", err)
		}
		if owner.Status().Desired != initial || owner.Status().Effective != initial || owner.Status().PendingRestart {
			t.Fatal("invalid candidate changed owner state")
		}
	}
	maximum := initial.Policy
	maximum.Timeout = time.Duration(maxCapability) * time.Millisecond
	maximum.MaxQueryBytes = int(maxCapability)
	maximum.WorkBudget.MaxQueryTerms = maxCapability
	next, err := ResolveSearchPolicy(owner.baseline, initial.Version.Generation+1, maximum)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.StageDesired(next); err != nil {
		t.Fatal(err)
	}
	if err := owner.Restart(next); err != nil {
		t.Fatal(err)
	}
	status, err := NewExecutor(newFakeBackend()).WithSearchPolicyOwner(owner).GetServerStatus(t.Context(), &pb.GetServerStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := status.GetSearch()
	if capabilities.GetTimeoutMs() != uint32(maxCapability) || capabilities.GetMaxQueryBytes() != uint32(maxCapability) || capabilities.GetMaxQueryTerms() != uint32(maxCapability) {
		t.Fatalf("maximum capability wrapped: %+v", capabilities)
	}
}
