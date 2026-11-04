package security

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestOperationalPolicyNativeMixedLogHistoryUpdatesAndRestart(t *testing.T) {
	n, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	public := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if err := public.BindLogPolicyRuntime(newLogPolicyRuntimeTestOwner()); err != nil {
		t.Fatal(err)
	}
	log := operationalPolicyApplyFixture(t, public, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelWarn}}))
	k := n.nodes[1].kernel
	legacy := k.origins[log.Original.HandoffDigest()]
	if legacy == nil || !bytes.HasPrefix([]byte(legacy.candidate), []byte(logPolicyCandidateMagic)) {
		t.Fatal("v1 logging envelope changed")
	}
	for _, group := range []operationalpolicy.Group{operationalpolicy.GroupSlowRPC, operationalpolicy.GroupSearch} {
		if err := public.BindOperationalPolicyRuntime(group, newOperationalPolicyRuntimeTestOwner(group)); err != nil {
			t.Fatal(err)
		}
		command := operationalPolicyRuntimeCommand(group)
		first := operationalPolicyApplyFixture(t, public, command)
		original := k.origins[first.Original.HandoffDigest()]
		if original == nil || !bytes.HasPrefix([]byte(original.candidate), []byte(operationalPolicyCandidateMagic)) || original.raw == original.candidate {
			t.Fatal("adoption lost original or support proof")
		}
		serial, count := k.originSerial, k.p.count
		found, err := public.Lookup(ctx, public.Profile(), original.handoff.id, original.handoff.operation.digest)
		if err != nil || found.Progress != CurrentApplied || found.Original != first.Original || k.originSerial != serial || k.p.count != count {
			t.Fatal("lookup reminted original or changed history", err)
		}
		floor := k.operationalFloors[group].floor
		command.Changes[0].OperationalPolicy.ExpectedGeneration = 1
		if group == operationalpolicy.GroupSlowRPC {
			command.Changes[0].OperationalPolicy.Values.SlowRPC.ThresholdMS = 1000
		} else {
			command.Changes[0].OperationalPolicy.Values.Search.TimeoutMS = 6000
		}
		second := operationalPolicyApplyFixture(t, public, command)
		if k.operationalFloors[group].floor != floor || k.replayState.projection.snapshot.OperationalPolicies().Get(group).Generation != 2 {
			t.Fatal("successor reset floor or generation")
		}
		proof := k.origins[second.Original.HandoffDigest()].candidate
		if (proof != "") != (group == operationalpolicy.GroupSearch) {
			t.Fatal("Search did not require every-update proof, or hot update remade adoption")
		}
	}
	before := k.operationalPolicyProjectionLocked()
	pf, bf, err := k.Floors()
	if err != nil {
		t.Fatal(err)
	}
	config := n.configs[1].Participant
	key, err := os.ReadFile(n.configs[1].Identity.VotingKey)
	if err != nil {
		t.Fatal(err)
	}
	config.Key = ed25519.PrivateKey(key)
	config.Trust = n.f.trust
	if err := n.nodes[1].Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeS2CParticipant(config, pf, bf)
	if err != nil {
		t.Fatal("complete mixed history did not restore", err)
	}
	defer func() {
		if err := resumed.Close(); err != nil {
			t.Error(err)
		}
	}()
	after := resumed.operationalPolicyProjectionLocked()
	if !reflect.DeepEqual(before, after) || resumed.replayState.projection.snapshot.LogPolicy().Level != LogLevelWarn || resumed.origins[log.Original.HandoffDigest()].candidate != legacy.candidate {
		t.Fatal("restart changed mixed desired state, floors or v1 bytes")
	}
	if !errors.Is(resumed.operationalOwnersAvailableLocked(), ErrOperationalPolicyUnavailable) {
		t.Fatal("restart invented actual runtime owner")
	}
}

func TestOperationalPolicyNativePartialPreparationCannotUseReserveOrBlockRevocation(t *testing.T) {
	n, owners, _ := authorityTestCompositeMembers(t, 3, 3, nil)
	ctx := s3aTestContext(t)
	public := &CurrentAuthority{origin: owners[1]}
	for id, origin := range owners {
		if id != 3 {
			if err := (&CurrentAuthority{origin: origin}).BindOperationalPolicyRuntime(operationalpolicy.GroupSlowRPC, newOperationalPolicyRuntimeTestOwner(operationalpolicy.GroupSlowRPC)); err != nil {
				t.Fatal(err)
			}
		}
		if err := origin.network.renewAuthority(ctx); err != nil {
			t.Fatal(err)
		}
	}
	producer := authorityFakeCredentialProducer{}
	a, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	review, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, operationalPolicyRuntimeCommand(operationalpolicy.GroupSlowRPC))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := public.Apply(ctx, review, producer, [32]byte{})
	if err == nil || pending.Progress != CurrentOriginDurable {
		t.Fatal("missing third actual owner allowed majority adoption", pending.Progress, err)
	}
	if n.nodes[1].kernel.operationalFloors[operationalpolicy.GroupSlowRPC] == nil || n.nodes[2].kernel.operationalFloors[operationalpolicy.GroupSlowRPC] == nil || n.nodes[3].kernel.operationalFloors[operationalpolicy.GroupSlowRPC] != nil {
		t.Fatal("partial PREPARED state misreported")
	}
	for id, node := range n.nodes {
		if node.kernel.replayState.projection.snapshot.OperationalPolicies().SlowRPC != nil {
			t.Fatal("partial support became desired", id)
		}
	}
	bob := s1Bob()
	result := operationalPolicyApplyFixture(t, public, s1Changes(Change{Kind: RevokeSessions, Identity: &bob}))
	if result.Original.Disposition() != S1Applied || n.nodes[1].kernel.replayState.projection.SessionLineage(bob) != 2 {
		t.Fatal("operational original blocked revocation")
	}
	serial := n.nodes[1].kernel.originSerial
	rejected, err := public.Lookup(ctx, review.Profile, review.ID, review.Operation.Digest())
	if err != nil || rejected.Progress != CurrentApplied || rejected.Original == nil || rejected.Original.Disposition() != S1RejectedCAS || n.nodes[1].kernel.originSerial != serial {
		t.Fatal("stale intent did not settle without remint", err)
	}
	if n.nodes[1].kernel.operationalFloors[operationalpolicy.GroupSlowRPC] == nil {
		t.Fatal("rejected original erased irreversible floor")
	}
}

func TestOperationalPolicyNativeCompletePreflightAndUncertainFloorNeverSign(t *testing.T) {
	n, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	o := owners[1]
	public := &CurrentAuthority{origin: o}
	ctx := s3aTestContext(t)
	if err := public.BindOperationalPolicyRuntime(operationalpolicy.GroupSlowRPC, newOperationalPolicyRuntimeTestOwner(operationalpolicy.GroupSlowRPC)); err != nil {
		t.Fatal(err)
	}
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	a, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	review, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, operationalPolicyRuntimeCommand(operationalpolicy.GroupSlowRPC))
	if err != nil {
		t.Fatal(err)
	}
	request, err := public.prepared(review)
	if err != nil {
		t.Fatal(err)
	}
	original, err := o.consume(ctx, request, producer, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	k := n.nodes[1].kernel
	before := k.p.floor
	limit := k.config.PendingBytes
	k.config.PendingBytes = 1
	k.gate.Lock()
	response, err := o.supportOperationalPolicyLocked(ctx, []byte(original.raw))
	k.gate.Unlock()
	k.config.PendingBytes = limit
	if err == nil || len(response.Voter.Signature) != 0 || k.p.floor != before || len(k.operationalFloors) != 0 {
		t.Fatal("incomplete successor budget fenced or signed", err)
	}
	k.hooks = &s2cParticipantHooks{beforePAppend: func(kind byte) {
		if kind == s2cPOperationalPolicyFloor {
			k.p.hooks = &s2cJournalHooks{afterLog: func() { panic("after durable append") }}
		}
	}}
	var signed operationalPolicySupportResponse
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		k.gate.Lock()
		defer k.gate.Unlock()
		defer k.poisonPanic()
		signed, _ = o.supportOperationalPolicyLocked(ctx, []byte(original.raw))
	}()
	if !panicked || len(signed.Voter.Signature) != 0 || len(signed.Origins) != 0 || len(k.operationalFloors) != 0 || k.readyLocked() == nil {
		t.Fatal("uncertain floor returned support or remained available")
	}
}
