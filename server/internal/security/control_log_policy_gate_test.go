package security

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

func TestLogPolicyNativeExplicitAdoptionAndVerifiedRestart(t *testing.T) {
	n, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	public := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	producer := authorityFakeCredentialProducer{}
	if err := public.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	admission, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := admission.CurrentCut()
	before := n.nodes[1].kernel.p.floor
	review, need, err := public.Prepare(ctx, admission, producer, public.Profile(), cut, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelWarn}}))
	if err != nil || need || n.nodes[1].kernel.p.floor != before {
		t.Fatal("review changed state or required unrelated purpose", err)
	}
	if result, err := public.Apply(ctx, review, producer, [32]byte{}); !errors.Is(err, ErrLogPolicyUnavailable) || result.Progress != CurrentUnresolved || n.nodes[1].kernel.p.floor != before {
		t.Fatal("absent actual owner consumed or fenced", result.Progress, err)
	}
	logger := newLogPolicyRuntimeTestOwner()
	if err := public.BindLogPolicyRuntime(logger); err != nil {
		t.Fatal(err)
	}
	result, err := public.Apply(ctx, review, producer, [32]byte{})
	if err != nil || result.Progress != CurrentApplied || result.Original.Disposition() != S1Applied {
		t.Fatal("native adoption failed", result.Progress, err)
	}
	k := n.nodes[1].kernel
	if k.logPolicyFloor.Index == 0 || k.logPolicyFloor.Chain == [32]byte{} || k.replayState.projection.snapshot.LogPolicy().Generation != 1 {
		t.Fatal("committed without durable supported policy")
	}
	original := k.origins[result.Original.HandoffDigest()]
	if original == nil || original.candidate == "" || original.raw == original.candidate {
		t.Fatal("original replaced or proof missing")
	}
	serial, count := k.originSerial, k.p.count
	again, err := public.Apply(ctx, review, nil, [32]byte{99})
	if err != nil || again.Original != result.Original || k.originSerial != serial || k.p.count != count {
		t.Fatal("replay consumed or changed original", err)
	}
	projection, err := public.ReconcileLogPolicy(ctx)
	if err != nil || projection.Policy.Level != LogLevelWarn || len(logger.applied) != 1 {
		t.Fatal("verified local application failed", err)
	}
	if err := public.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	otherAdmission, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	otherCut, _ := otherAdmission.CurrentCut()
	otherReview, _, err := public.Prepare(ctx, otherAdmission, producer, public.Profile(), otherCut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	other, err := public.Apply(ctx, otherReview, producer, [32]byte{})
	if err != nil || other.Progress != CurrentApplied {
		t.Fatal("ordinary security edit after policy failed", err)
	}
	projection, err = public.CurrentLogPolicy(ctx)
	if err != nil || projection.Policy.Generation != 1 {
		t.Fatal("unrelated edit changed policy", err)
	}
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
	if err = n.nodes[1].Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeS2CParticipant(config, pf, bf)
	if err != nil {
		t.Fatal("verified complete recovery rejected policy/floor", err)
	}
	defer resumed.Close()
	if resumed.logPolicyFloor != k.logPolicyFloor || resumed.logPolicyFloorOriginal != k.logPolicyFloorOriginal || *resumed.replayState.projection.snapshot.LogPolicy() != *projection.Policy {
		t.Fatal("restart lost committed ancestry or version floor")
	}
	if resumed.origins[result.Original.HandoffDigest()].raw != original.raw || resumed.origins[result.Original.HandoffDigest()].candidate != original.candidate {
		t.Fatal("restart changed original or support certificate")
	}
}

func TestLogPolicyNativeEveryVoterAndOriginRequiredAfterPartialFence(t *testing.T) {
	n, owners, _ := authorityTestCompositeMembers(t, 3, 3, nil)
	ctx := s3aTestContext(t)
	publics := map[uint32]*CurrentAuthority{}
	for id, owner := range owners {
		publics[id] = &CurrentAuthority{origin: owner}
		if id != 3 {
			if err := publics[id].BindLogPolicyRuntime(newLogPolicyRuntimeTestOwner()); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, owner := range owners {
		if err := owner.network.renewAuthority(ctx); err != nil {
			t.Fatal(err)
		}
	}
	public := publics[1]
	producer := authorityFakeCredentialProducer{}
	a, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	review, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelDebug}}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := public.Apply(ctx, review, producer, [32]byte{})
	if err == nil || result.Progress != CurrentOriginDurable {
		t.Fatal("missing actual third owner treated as majority adoption", result.Progress, err)
	}
	for id, node := range n.nodes {
		if node.kernel.replayState.projection.snapshot.LogPolicy() != nil || node.kernel.replayState.slot != 0 {
			t.Fatal("partial support committed", id)
		}
	}
	if n.nodes[1].kernel.logPolicyFloor.Index == 0 || n.nodes[2].kernel.logPolicyFloor.Index == 0 || n.nodes[3].kernel.logPolicyFloor.Index != 0 {
		t.Fatal("partial durable preparation misreported")
	}
	serial := n.nodes[1].kernel.originSerial
	var original string
	for _, h := range n.nodes[1].kernel.origins {
		if h.handoff.id == review.ID {
			original = h.raw
		}
	}
	if err := publics[3].BindLogPolicyRuntime(newLogPolicyRuntimeTestOwner()); err != nil {
		t.Fatal(err)
	}
	result, err = public.Apply(ctx, review, nil, [32]byte{})
	if err != nil || result.Progress != CurrentApplied || n.nodes[1].kernel.originSerial != serial {
		t.Fatal("retained first intent did not recover without new consume", result.Progress, err)
	}
	for id, node := range n.nodes {
		if node.kernel.logPolicyFloor.Index == 0 {
			t.Fatal("activated without all durable floors", id)
		}
	}
	if n.nodes[1].kernel.origins[result.Original.HandoffDigest()].raw != original {
		t.Fatal("recovery restamped original")
	}
}

func TestLogPolicyNativePartialPreparationCannotBlockRevocation(t *testing.T) {
	network, owners, _ := authorityTestCompositeMembers(t, 3, 3, nil)
	ctx := s3aTestContext(t)
	public := &CurrentAuthority{origin: owners[1]}
	for id, owner := range owners {
		if id != 3 {
			if err := (&CurrentAuthority{origin: owner}).BindLogPolicyRuntime(newLogPolicyRuntimeTestOwner()); err != nil {
				t.Fatal(err)
			}
		}
		if err := owner.network.renewAuthority(ctx); err != nil {
			t.Fatal(err)
		}
	}
	producer := authorityFakeCredentialProducer{}
	a, err := public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	review, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelDebug}}))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := public.Apply(ctx, review, producer, [32]byte{})
	if err == nil || pending.Progress != CurrentOriginDurable {
		t.Fatal("expected bounded incomplete adoption", err)
	}
	bob := s1Bob()
	a, err = public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	currentCut, _ := a.CurrentCut()
	revoke, _, err := public.Prepare(ctx, a, producer, public.Profile(), currentCut, s1Changes(Change{Kind: RevokeSessions, Identity: &bob}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := public.Apply(ctx, revoke, producer, [32]byte{})
	if err != nil || result.Progress != CurrentApplied || result.Original.Disposition() != S1Applied {
		t.Fatal("incomplete operational intent blocked revocation", err)
	}
	if network.nodes[1].kernel.replayState.projection.SessionLineage(bob) != 2 {
		t.Fatal("revocation did not advance lineage")
	}
	serial := network.nodes[1].kernel.originSerial
	rejected, err := public.Lookup(ctx, review.Profile, review.ID, review.Operation.Digest())
	if err != nil || rejected.Progress != CurrentApplied || rejected.Original == nil || rejected.Original.Disposition() != S1RejectedCAS {
		t.Fatal("stale original did not settle without all support", rejected.Progress, err)
	}
	if network.nodes[1].kernel.originSerial != serial || network.nodes[1].kernel.replayState.projection.snapshot.LogPolicy() != nil {
		t.Fatal("terminal rejection reminted or activated intent")
	}
	if network.nodes[1].kernel.logPolicyFloor.Index == 0 {
		t.Fatal("terminal rejection erased irreversible local version floor")
	}
}

func TestLogPolicyNativeCorruptPersistedPolicyRefusesRestore(t *testing.T) {
	network, authority, _, _ := logPolicyRuntimeCommittedFixture(t, LogLevelWarn)
	k := authority.origin.network.kernel
	pf, bf, err := k.Floors()
	if err != nil {
		t.Fatal(err)
	}
	config := network.configs[1].Participant
	key, err := os.ReadFile(network.configs[1].Identity.VotingKey)
	if err != nil {
		t.Fatal(err)
	}
	config.Key = ed25519.PrivateKey(key)
	config.Trust = network.f.trust
	if err := network.nodes[1].Close(); err != nil {
		t.Fatal(err)
	}
	wal := s2RestoreTestReadFile(t, config.BPath)
	offset, last := 8, 8
	for offset < len(wal) {
		last = offset
		offset += 8 + int(binary.BigEndian.Uint32(wal[offset:]))
	}
	payload := bytes.Clone(wal[last+44:])
	changed := bytes.Replace(payload, []byte(`"level":"warn"`), []byte(`"level":"nope"`), 1)
	if bytes.Equal(changed, payload) {
		t.Fatal("actual persisted log policy was not located")
	}
	// Preserve physical CRC and the final tip hash: only authenticated typed
	// history may restore policy; valid framing cannot authorize corruption.
	s2RestoreTestReplaceFinalPayload(t, config.BPath, changed)
	corrupt := s2RestoreTestReadFile(t, config.BPath)
	tip := s2RestoreTestReadFile(t, config.BPath+".tip")
	if restored, err := resumeS2CParticipant(config, pf, bf); err == nil {
		_ = restored.Close()
		t.Fatal("corrupt committed log policy became serving state or env fallback")
	}
	if !bytes.Equal(corrupt, s2RestoreTestReadFile(t, config.BPath)) || !bytes.Equal(tip, s2RestoreTestReadFile(t, config.BPath+".tip")) {
		t.Fatal("failed restore repaired/truncated corrupt adopted state")
	}
}

func TestLogPolicyNativeReplayRejectsAuthenticButStaleFloor(t *testing.T) {
	network, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	o := owners[1]
	public := &CurrentAuthority{origin: o}
	ctx := s3aTestContext(t)
	if err := public.BindLogPolicyRuntime(newLogPolicyRuntimeTestOwner()); err != nil {
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
	review, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelDebug}}))
	if err != nil {
		t.Fatal(err)
	}
	request, err := public.prepared(review)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := o.consume(ctx, request, producer, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	bob := s1Bob()
	a, err = public.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ = a.CurrentCut()
	revoke, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, s1Changes(Change{Kind: RevokeSessions, Identity: &bob}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := public.Apply(ctx, revoke, producer, [32]byte{})
	if err != nil || result.Progress != CurrentApplied {
		t.Fatal(err)
	}
	k := o.network.kernel
	old, err := verifyAuthorityHistoricalH(k.trust, []byte(retained.raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := logPolicyFloorBytes(old)
	if err != nil {
		t.Fatal(err)
	}
	framed, err := k.encodeRecord(s2cPRecord{kind: s2cPLogPolicyFloor, index: k.p.count + 1, raw: string(body)})
	if err != nil {
		t.Fatal(err)
	}
	// This exact signed H is authentic, but live support would refuse its old
	// reviewed cut. Physically correct history cannot bypass that side effect
	// authorization by moving the marker after a real revocation.
	if _, err = k.p.append(framed, 0, 0); err != nil {
		t.Fatal(err)
	}
	pf, bf, err := k.Floors()
	if err != nil {
		t.Fatal(err)
	}
	config := network.configs[1].Participant
	key, err := os.ReadFile(network.configs[1].Identity.VotingKey)
	if err != nil {
		t.Fatal(err)
	}
	config.Key = ed25519.PrivateKey(key)
	config.Trust = network.f.trust
	if err = network.nodes[1].Close(); err != nil {
		t.Fatal(err)
	}
	before := s2RestoreTestReadFile(t, config.PPath)
	if restored, err := resumeS2CParticipant(config, pf, bf); err == nil {
		_ = restored.Close()
		t.Fatal("authenticated stale original authorized impossible replay floor")
	}
	if !bytes.Equal(before, s2RestoreTestReadFile(t, config.PPath)) {
		t.Fatal("semantic marker refusal changed retained history")
	}
}
