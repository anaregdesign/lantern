package security

import "testing"

func TestLogPolicyNativePreflightAndUncertainFloorNeverSign(t *testing.T) {
	n, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
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
	review, _, err := public.Prepare(ctx, a, producer, public.Profile(), cut, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelError}}))
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
	pendingLimit := k.config.PendingBytes
	k.config.PendingBytes = 1
	k.gate.Lock()
	response, err := o.supportLogPolicyLocked(ctx, []byte(original.raw))
	k.gate.Unlock()
	k.config.PendingBytes = pendingLimit
	if err == nil || len(response.Voter.Signature) != 0 || k.p.floor != before || k.logPolicyFloor.Index != 0 {
		t.Fatal("insufficient complete budget fenced or signed", err)
	}
	k.hooks = &s2cParticipantHooks{beforePAppend: func(kind byte) {
		if kind == s2cPLogPolicyFloor {
			k.p.hooks = &s2cJournalHooks{afterLog: func() { panic("after durable append") }}
		}
	}}
	var signed logPolicySupportResponse
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
		signed, _ = o.supportLogPolicyLocked(ctx, []byte(original.raw))
	}()
	if !panicked || len(signed.Voter.Signature) != 0 || len(signed.Origins) != 0 || k.logPolicyFloor.Index != 0 || k.readyLocked() == nil {
		t.Fatal("uncertain floor returned support or remained ready")
	}
}
