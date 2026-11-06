package security

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

// These owners are isolated fixtures, never production capability declarations.
type operationalPolicyRuntimeTestOwner struct {
	mu                                 sync.Mutex
	group                              operationalpolicy.Group
	availableErr, prepareErr, applyErr error
	fingerprint                        string
	applied                            *operationalpolicy.Policy
	onApply                            func() error
}

func newOperationalPolicyRuntimeTestOwner(group operationalpolicy.Group) *operationalPolicyRuntimeTestOwner {
	return &operationalPolicyRuntimeTestOwner{group: group, fingerprint: strings.Repeat("a", 64)}
}
func (o *operationalPolicyRuntimeTestOwner) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !group.Valid() || o.group != group || version != operationalpolicy.Version {
		return ErrOperationalPolicyUnavailable
	}
	return o.availableErr
}
func (o *operationalPolicyRuntimeTestOwner) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	group, err := change.Values.Group()
	if err != nil || group != o.group || change.Validate() != nil || group == operationalpolicy.GroupSearch && change.CompatibilityFingerprint != o.fingerprint {
		return ErrOperationalPolicyUnavailable
	}
	return o.prepareErr
}
func (o *operationalPolicyRuntimeTestOwner) PreviewOperationalPolicy(values operationalpolicy.Values) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	group, err := values.Group()
	if err != nil || values.Validate() != nil || group != o.group {
		return "", ErrOperationalPolicyUnavailable
	}
	return o.fingerprint, nil
}
func (o *operationalPolicyRuntimeTestOwner) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	o.mu.Lock()
	callback := o.onApply
	o.mu.Unlock()
	if callback != nil {
		if err := callback(); err != nil {
			return err
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	group, err := policy.Values.Group()
	if policy.Validate() != nil || err != nil || group != o.group || o.applied != nil && (policy.Generation < o.applied.Generation || policy.Generation == o.applied.Generation && !reflect.DeepEqual(&policy, o.applied)) {
		return ErrOperationalPolicyUnavailable
	}
	if o.applyErr != nil {
		return o.applyErr
	}
	o.applied = policy.Clone()
	return nil
}

func operationalPolicyRuntimeCommand(group operationalpolicy.Group) S1Command {
	values, _ := operationalpolicy.Defaults(group)
	change := &operationalpolicy.Change{Values: values}
	if group == operationalpolicy.GroupSearch {
		change.CompatibilityFingerprint = strings.Repeat("a", 64)
	}
	if group == operationalpolicy.GroupBackup {
		change.RetentionReductionAcknowledged = true
	}
	return s1Changes(Change{Kind: PutOperationalPolicy, OperationalPolicy: change})
}

func operationalPolicyApplyFixture(t *testing.T, public *CurrentAuthority, command S1Command) CurrentChangeResult {
	t.Helper()
	ctx := s3aTestContext(t)
	if err := public.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	admission, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := admission.CurrentCut()
	review, _, err := public.Prepare(ctx, admission, producer, public.Profile(), cut, command)
	if err != nil {
		t.Fatal(err)
	}
	result, err := public.Apply(ctx, review, producer, [32]byte{})
	initialProgress, initialErr := result.Progress, err
	result, err = operationalPolicyCompleteFixture(ctx, review, result, err, public.Lookup,
		operationalPolicyRetainedFixture(public, review), func(ctx context.Context) bool {
			return s3aPause(ctx, public.origin.network.limits.Backoff)
		})
	if err != nil {
		t.Fatal("reviewed native commit failed", "initial", initialProgress, initialErr, "final", result.Progress, err)
	}
	return result
}

// A bounded scheduling refusal can retain H without applying it. Continue only
// that exact original through status lookup, within the first invocation's
// context. This fixture never prepares, renews, consumes or calls Apply again.
func operationalPolicyCompleteFixture(ctx context.Context, review CurrentReview, result CurrentChangeResult, err error,
	lookup func(context.Context, CurrentProfile, FullChangeID, [32]byte) (CurrentChangeResult, error),
	retained func(CurrentChangeResult) error, pause func(context.Context) bool,
) (CurrentChangeResult, error) {
	for {
		if ctx.Err() != nil {
			return result, errors.Join(err, ctx.Err())
		}
		if _, bounded := ctx.Deadline(); !bounded {
			return result, errors.Join(err, ErrS1Contract)
		}
		if result.Profile != review.Profile || result.ID != review.ID || result.Intent != review.Operation.Digest() {
			return result, errors.Join(err, ErrS1Contract)
		}
		if result.Progress == CurrentApplied {
			if err != nil || result.Original == nil || result.Original.ID() != review.ID || result.Original.Operation() != review.Operation || result.Original.Disposition() != S1Applied {
				return result, errors.Join(err, ErrS1Contract)
			}
			return result, retained(result)
		}
		if !operationalPolicyCreditOnlyFixture(err) || result.Original != nil || result.Progress != CurrentOriginDurable && result.Progress != CurrentChosen {
			return result, errors.Join(err, ErrS1Contract)
		}
		if checkErr := retained(result); checkErr != nil {
			return result, checkErr
		}
		if !pause(ctx) {
			return result, errors.Join(err, ctx.Err())
		}
		if ctx.Err() != nil {
			return result, errors.Join(err, ctx.Err())
		}
		result, err = lookup(ctx, review.Profile, review.ID, review.Operation.Digest())
	}
}

// errors.Is alone would also accept credit joined with cancellation, authority
// loss or an unknown failure. Every actual error leaf must be the credit refusal.
func operationalPolicyCreditOnlyFixture(err error) bool {
	if err == errS3ACredit {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !operationalPolicyCreditOnlyFixture(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return operationalPolicyCreditOnlyFixture(wrapped.Unwrap())
	}
	return false
}

func operationalPolicyRetainedFixture(public *CurrentAuthority, review CurrentReview) func(CurrentChangeResult) error {
	k := public.origin.network.kernel
	var captured bool
	var serial uint64
	var reservation authorityOriginReservation
	var digest [32]byte
	var raw string
	return func(result CurrentChangeResult) error {
		k.gate.Lock()
		defer k.gate.Unlock()
		owned := k.originIDs[review.ID]
		current, ok := k.originReservations[owned]
		if !ok || owned == 0 || current.ID != review.ID || current.Operation != review.Operation.Digest() {
			return errors.New("fixture lost original reservation ownership")
		}
		var h *s2cHistoricalH
		for _, original := range k.origins {
			if original.handoff.id == review.ID && original.handoff.operation == review.Operation {
				h = original
				break
			}
		}
		if h == nil || h.handoff.serial != owned {
			return errors.New("fixture lost retained original H")
		}
		if !captured {
			captured, serial, reservation, digest, raw = true, k.originSerial, current, h.digest(), h.raw
		}
		if k.originSerial != serial || current != reservation || h.digest() != digest || h.raw != raw || result.Original != nil && result.Original.HandoffDigest() != digest {
			return errors.New("fixture changed original H or reservation ownership")
		}
		return nil
	}
}

func TestOperationalPolicyRuntimeBindingAndVerifiedProjection(t *testing.T) {
	_, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	public := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	group := operationalpolicy.GroupSlowRPC
	if err := public.BindOperationalPolicyRuntime(group, nil); !errors.Is(err, ErrOperationalPolicyUnavailable) {
		t.Fatal("nil owner bound", err)
	}
	if err := public.BindOperationalPolicyRuntime(group, newOperationalPolicyRuntimeTestOwner(operationalpolicy.GroupSearch)); err == nil {
		t.Fatal("wrong actual group bound")
	}
	owner := newOperationalPolicyRuntimeTestOwner(group)
	if err := public.BindOperationalPolicyRuntime(group, owner); err != nil {
		t.Fatal(err)
	}
	if err := public.BindOperationalPolicyRuntime(group, newOperationalPolicyRuntimeTestOwner(group)); err == nil {
		t.Fatal("bound owner replaced")
	}
	if p, err := public.ReconcileOperationalPolicies(ctx); err != nil || p.Policies.Get(group) != nil || owner.applied != nil {
		t.Fatal("unadopted values invented", err)
	}
	operationalPolicyApplyFixture(t, public, operationalPolicyRuntimeCommand(group))
	p, err := public.CurrentOperationalPolicies(ctx)
	if err != nil || p.Policies.SlowRPC == nil || len(p.Preparations) != 1 || p.Preparations[0].Group != group || p.Policies.SlowRPC.Generation != 1 || owner.applied != nil {
		t.Fatal("commit claimed application or lost desired", err)
	}
	p.Policies.SlowRPC.Values.SlowRPC.ThresholdMS = 999
	p.Preparations[0].Group = operationalpolicy.GroupRate
	next, err := public.ReconcileOperationalPolicies(ctx)
	if err != nil || next.Policies.SlowRPC.Values.SlowRPC.ThresholdMS != 500 || next.Preparations[0].Group != group || owner.applied == nil {
		t.Fatal("projection aliases native state or did not reconcile", err)
	}
	owner.applyErr = errors.New("fixture apply failure")
	if got, err := public.ReconcileOperationalPolicies(ctx); !errors.Is(err, ErrOperationalPolicyUnavailable) || got.Policies.SlowRPC.Generation != 1 {
		t.Fatal("application failure lost desired or fell back", err)
	}
	owner.availableErr = errors.New("fixture owner unavailable")
	if err := public.RequireOperationalPolicyOwners(ctx); !errors.Is(err, ErrOperationalPolicyUnavailable) {
		t.Fatal("missing required owner accepted", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := public.CurrentOperationalPolicies(cancelled); err == nil {
		t.Fatal("cancelled projection accepted")
	}
	if _, err := (*CurrentAuthority)(nil).CurrentOperationalPolicies(ctx); err == nil {
		t.Fatal("absent authority fabricated unadopted state")
	}
}

func TestOperationalPolicyRuntimePreviewIsReadOnlyAndCutBound(t *testing.T) {
	_, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	public := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	group := operationalpolicy.GroupSearch
	owner := newOperationalPolicyRuntimeTestOwner(group)
	if err := public.BindOperationalPolicyRuntime(group, owner); err != nil {
		t.Fatal(err)
	}
	if err := public.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	a, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	values, _ := operationalpolicy.Defaults(group)
	k := public.origin.network.kernel
	before := k.p.floor
	serial := k.originSerial
	fingerprint, err := public.PreviewOperationalPolicy(ctx, a, values)
	if err != nil || fingerprint != owner.fingerprint || k.p.floor != before || k.originSerial != serial || len(k.operationalFloors) != 0 || owner.applied != nil {
		t.Fatal("preview changed native/runtime state", err)
	}
	command := operationalPolicyRuntimeCommand(group)
	command.Changes[0].OperationalPolicy.CompatibilityFingerprint = fingerprint
	owner.fingerprint = strings.Repeat("b", 64)
	cut, _ := a.CurrentCut()
	review, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := public.Apply(ctx, review, producer, [32]byte{}); !errors.Is(err, ErrOperationalPolicyUnavailable) || k.p.floor != before || k.originSerial != serial {
		t.Fatal("stale preview consumed H or applied", err)
	}
	operationalPolicyApplyFixture(t, public, s1Changes(s1ReaderRole()))
	if _, err := public.OperationalPoliciesForAdmission(ctx, a); err == nil {
		t.Fatal("stale security cut read desired state")
	}
}
