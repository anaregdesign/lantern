package security

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestOperationalPolicyFixtureContinuation(t *testing.T) {
	review := CurrentReview{Profile: CurrentProfile{Version: CurrentPublicVersion}, ID: FullChangeID{Nonce: [16]byte{1}}, Operation: OperationIdentity{digest: [32]byte{2}}}
	pending := CurrentChangeResult{Profile: review.Profile, ID: review.ID, Intent: review.Operation.Digest(), Progress: CurrentOriginDurable}
	applied := pending
	applied.Progress = CurrentApplied
	applied.Original = &OriginalOutcome{id: review.ID, operation: review.Operation, handoff: [32]byte{3}, disposition: S1Applied}
	t.Run("same original and context through bounded pending", func(t *testing.T) {
		ctx := s3aTestContext(t)
		lookups, checks, pauses := 0, 0, 0
		result, err := operationalPolicyCompleteFixture(ctx, review, pending, fmt.Errorf("bounded scheduling: %w", errS3ACredit),
			func(got context.Context, profile CurrentProfile, id FullChangeID, intent [32]byte) (CurrentChangeResult, error) {
				lookups++
				if got != ctx || profile != review.Profile || id != review.ID || intent != review.Operation.Digest() {
					t.Fatal("lookup changed the original invocation")
				}
				if lookups == 1 {
					chosen := pending
					chosen.Progress = CurrentChosen
					return chosen, errors.Join(errS3ACredit, errS3ACredit)
				}
				return applied, nil
			}, func(result CurrentChangeResult) error {
				checks++
				if result.Original != nil && result.Original.HandoffDigest() != applied.Original.HandoffDigest() {
					t.Fatal("original handoff changed")
				}
				return nil
			}, func(got context.Context) bool {
				pauses++
				if got != ctx {
					t.Fatal("backoff reset the invocation context")
				}
				return true
			})
		if err != nil || result != applied || lookups != 2 || checks != 3 || pauses != 2 {
			t.Fatal("exact pending completion changed", result.Progress, err, lookups, checks, pauses)
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(*CurrentChangeResult)
		err    error
	}{
		{"refusal", nil, ErrAuthorityUnavailable},
		{"mixed cancellation", nil, errors.Join(errS3ACredit, context.Canceled)},
		{"mixed authority", nil, errors.Join(errS3ACredit, ErrAuthorityUnavailable)},
		{"mixed unknown", nil, errors.Join(errS3ACredit, errors.New("unknown"))},
		{"unresolved", func(r *CurrentChangeResult) { r.Progress = CurrentUnresolved }, errS3ACredit},
		{"nil pending error", nil, nil},
		{"wrong profile", func(r *CurrentChangeResult) { r.Profile.Version++ }, errS3ACredit},
		{"wrong ID", func(r *CurrentChangeResult) { r.ID.Nonce[0]++ }, errS3ACredit},
		{"wrong intent", func(r *CurrentChangeResult) { r.Intent[0]++ }, errS3ACredit},
		{"nil applied outcome", func(r *CurrentChangeResult) { r.Progress = CurrentApplied }, nil},
		{"applied with error", func(r *CurrentChangeResult) { *r = applied }, errS3ACredit},
		{"rejected outcome", func(r *CurrentChangeResult) {
			*r = applied
			rejected := *applied.Original
			rejected.disposition = S1RejectedCAS
			r.Original = &rejected
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := pending
			if tc.mutate != nil {
				tc.mutate(&result)
			}
			_, err := operationalPolicyCompleteFixture(s3aTestContext(t), review, result, tc.err,
				func(context.Context, CurrentProfile, FullChangeID, [32]byte) (CurrentChangeResult, error) {
					t.Fatal("refused invocation entered lookup")
					return CurrentChangeResult{}, nil
				}, func(CurrentChangeResult) error { t.Fatal("refused invocation inspected H"); return nil },
				func(context.Context) bool { t.Fatal("refused invocation waited"); return false })
			if err == nil {
				t.Fatal("refused invocation completed")
			}
		})
	}
	for _, phase := range []string{"lookup mismatch", "lookup mixed error", "retained mismatch", "cancelled", "cancel while waiting", "cancel in lookup", "expired", "unbounded", "backoff exhausted"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(s3aTestContext(t))
			defer cancel()
			var input context.Context = ctx
			if phase == "cancelled" {
				cancel()
			}
			if phase == "expired" {
				expired, stop := context.WithDeadline(t.Context(), time.Unix(1, 0))
				defer stop()
				input = expired
			}
			if phase == "unbounded" {
				input = context.Background()
			}
			lookups, pauses := 0, 0
			_, err := operationalPolicyCompleteFixture(input, review, pending, errS3ACredit,
				func(context.Context, CurrentProfile, FullChangeID, [32]byte) (CurrentChangeResult, error) {
					lookups++
					if phase == "lookup mismatch" {
						wrong := applied
						wrong.ID.Nonce[0]++
						return wrong, nil
					}
					if phase == "cancel in lookup" {
						cancel()
						return pending, errS3ACredit
					}
					return pending, errors.Join(errS3ACredit, ErrAuthorityUnavailable)
				}, func(CurrentChangeResult) error {
					if phase == "retained mismatch" {
						return ErrChangeConflict
					}
					return nil
				}, func(context.Context) bool {
					pauses++
					if phase == "cancel while waiting" {
						cancel()
					}
					return phase != "backoff exhausted"
				})
			wantLookups, wantPauses := 0, 0
			if phase == "lookup mismatch" || phase == "lookup mixed error" || phase == "cancel in lookup" {
				wantLookups, wantPauses = 1, 1
			}
			if phase == "backoff exhausted" || phase == "cancel while waiting" {
				wantPauses = 1
			}
			if err == nil || lookups != wantLookups || pauses != wantPauses {
				t.Fatal("refusal did not stay bounded", err, lookups, pauses)
			}
			if (phase == "cancelled" || phase == "cancel while waiting" || phase == "cancel in lookup") && !errors.Is(err, context.Canceled) || phase == "expired" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("original context failure changed", err)
			}
		})
	}
}

func TestOperationalPolicyFixtureRetainedIdentity(t *testing.T) {
	review := CurrentReview{ID: FullChangeID{Nonce: [16]byte{1}}, Operation: OperationIdentity{digest: [32]byte{2}}}
	h := &s2cHistoricalH{raw: "original immutable H", handoff: &S1Handoff{id: review.ID, operation: review.Operation, serial: 1, authorization: &s1VerifiedAuthorization{}}}
	reservation := authorityOriginReservation{Serial: 1, ID: review.ID, Operation: review.Operation.Digest()}
	k := &s2cParticipant{originSerial: 1, originIDs: map[FullChangeID]uint64{review.ID: 1}, originReservations: map[uint64]authorityOriginReservation{1: reservation}, origins: map[[32]byte]*s2cHistoricalH{h.digest(): h}}
	public := &CurrentAuthority{origin: &authorityOriginOwner{network: &s3aOwner{kernel: k}}}
	pending := CurrentChangeResult{Progress: CurrentOriginDurable}
	t.Run("support completion may advance", func(t *testing.T) {
		check := operationalPolicyRetainedFixture(public, review)
		if err := check(pending); err != nil {
			t.Fatal(err)
		}
		h.candidate = "completed support proof"
		defer func() { h.candidate = "" }()
		if err := check(pending); err != nil {
			t.Fatal("legitimate completion metadata was frozen", err)
		}
	})
	for _, phase := range []string{"serial", "reservation", "H raw", "H identity", "outcome handoff"} {
		t.Run(phase, func(t *testing.T) {
			check := operationalPolicyRetainedFixture(public, review)
			if err := check(pending); err != nil {
				t.Fatal(err)
			}
			result := pending
			switch phase {
			case "serial":
				k.originSerial++
				defer func() { k.originSerial-- }()
			case "reservation":
				changed := reservation
				changed.Descriptor[0]++
				k.originReservations[1] = changed
				defer func() { k.originReservations[1] = reservation }()
			case "H raw":
				h.raw += " changed"
				defer func() { h.raw = "original immutable H" }()
			case "H identity":
				h.handoff.id.Nonce[0]++
				defer func() { h.handoff.id = review.ID }()
			case "outcome handoff":
				result.Original = &OriginalOutcome{handoff: [32]byte{99}}
			}
			if err := check(result); err == nil {
				t.Fatal("changed original was accepted")
			}
		})
	}
}

func TestOperationalPolicyNativeRetainedCreditCompletion(t *testing.T) {
	n, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	public := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if err := public.BindOperationalPolicyRuntime(operationalpolicy.GroupSlowRPC, newOperationalPolicyRuntimeTestOwner(operationalpolicy.GroupSlowRPC)); err != nil {
		t.Fatal(err)
	}
	if err := public.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	admission, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := admission.CurrentCut()
	review, _, err := public.Prepare(ctx, admission, producer, public.Profile(), cut, operationalPolicyRuntimeCommand(operationalpolicy.GroupSlowRPC))
	if err != nil {
		t.Fatal(err)
	}
	network := public.origin.network
	network.driveSlot <- struct{}{}
	held := true
	release := func() {
		if held {
			<-network.driveSlot
			held = false
		}
	}
	defer release()
	pending, err := public.Apply(ctx, review, producer, [32]byte{})
	if !operationalPolicyCreditOnlyFixture(err) || pending.Progress != CurrentOriginDurable || pending.Original != nil {
		t.Fatal("held drive slot did not retain the original", pending.Progress, err)
	}
	check := operationalPolicyRetainedFixture(public, review)
	if err := check(pending); err != nil {
		t.Fatal(err)
	}
	k := n.nodes[1].kernel
	k.gate.Lock()
	serial := k.originSerial
	k.gate.Unlock()
	release()
	lookups := 0
	result, err := operationalPolicyCompleteFixture(ctx, review, pending, err,
		func(got context.Context, profile CurrentProfile, id FullChangeID, intent [32]byte) (CurrentChangeResult, error) {
			lookups++
			if got != ctx {
				t.Fatal("recovery reset the original deadline")
			}
			return public.Lookup(got, profile, id, intent)
		}, check, func(ctx context.Context) bool { return s3aPause(ctx, network.limits.Backoff) })
	k.gate.Lock()
	sameSerial := k.originSerial == serial
	policy := k.operationalPolicyProjectionLocked().Policies.SlowRPC
	k.gate.Unlock()
	if err != nil || lookups == 0 || result.Progress != CurrentApplied || result.Original == nil || !sameSerial || policy == nil || policy.Generation != 1 {
		t.Fatal("same-ID recovery did not finish the retained native original", result.Progress, err, lookups)
	}
}
