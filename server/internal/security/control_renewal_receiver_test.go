package security

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestAuthorityRenewalReceiver(t *testing.T) {
	for _, mode := range []string{"majority", "duplicate", "expired", "wrong request", "prefix changed", "restart", "closed"} {
		t.Run(mode, func(t *testing.T) {
			n := s2cTestNativeCluster(t, 3, nil)
			clock, ticks := fakeAuthorityTimeOwner(t)
			r := &authorityRenewalReceiver{kernel: n.nodes[1], clock: clock, workloads: [32]byte{7}}
			raw, err := r.challenge()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.challenge(); err == nil {
				t.Fatal("multiple outstanding challenges")
			}
			var votes [][]byte
			for _, id := range []uint32{1, 2} {
				v, err := n.nodes[id].signAuthorityRenewal(raw, r.workloads, func() error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				votes = append(votes, v)
			}
			if err = r.receive(raw, votes[0]); err != nil {
				t.Fatal(err)
			}
			if r.active != nil {
				t.Fatal("minority activated")
			}
			switch mode {
			case "duplicate":
				if err = r.receive(raw, votes[0]); err == nil || r.active != nil {
					t.Fatal("duplicate member counted")
				}
			case "expired":
				ticks.Add(authorityRenewalLifetime)
				if err = r.receive(raw, votes[1]); err == nil || r.pending != nil || r.active != nil {
					t.Fatal("late arrival renewed lifetime")
				}
				return
			case "wrong request":
				wrong := append([]byte(nil), raw...)
				wrong[len(wrong)-1] ^= 1
				if err = r.receive(wrong, votes[1]); err == nil {
					t.Fatal("response rebound")
				}
			case "prefix changed":
				n.selectValue(1, [32]byte{}, 1, 2)
				n.choose(1, 1, 2)
				if err = r.receive(raw, votes[1]); err == nil || r.pending != nil {
					t.Fatal("old pending window survived NOOP")
				}
				return
			case "restart":
				r = &authorityRenewalReceiver{kernel: n.nodes[1], clock: clock, workloads: r.workloads}
				if err = r.receive(raw, votes[1]); err == nil {
					t.Fatal("decoded votes restored capability")
				}
				return
			case "closed":
				r.close()
				if err = r.receive(raw, votes[1]); err == nil {
					t.Fatal("closed receiver activated")
				}
				return
			}
			if err = r.receive(raw, votes[1]); err != nil || r.active == nil || r.pending != nil {
				t.Fatal("distinct majority did not activate", err)
			}
			n.nodes[1].gate.Lock()
			now, active, err := r.currentLocked()
			n.nodes[1].gate.Unlock()
			if err != nil || active == nil || now.owner != clock {
				t.Fatal("current capability", err)
			}
			encoded := active.certificate.raw
			if _, err := parseAuthorityRenewalCertificate([]byte(encoded), n.fixture.trust, r.workloads); err != nil {
				t.Fatal(err)
			}
			ticks.Add(authorityRenewalLifetime)
			n.nodes[1].gate.Lock()
			_, _, err = r.currentLocked()
			n.nodes[1].gate.Unlock()
			if err == nil {
				t.Fatal("expired active window used")
			}
		})
	}
}

func TestAuthorityRenewalDiagnosticDistinguishesActualRefusal(t *testing.T) {
	for _, reason := range []string{"closed", "recovery", "configuration", "kernel", "no_certificate", "binding", "clock", "elapsed", "expired"} {
		t.Run(reason, func(t *testing.T) {
			n, _ := authorityTestNativeCluster(t, 3)
			r, ticks := authorityTestReceiver(t, n)
			// This existing diagnostic identity is not an authority input.
			r.clock.traceID = 71
			switch reason {
			case "closed":
				r.closed = true
			case "recovery":
				r.startupRecoveryPending = true
			case "configuration":
				r.kernel.config.configured = &ConfiguredTrust{}
			case "kernel":
				r.kernel.unknown = errS2CUnknown
			case "no_certificate":
				r.active = nil
			case "binding":
				r.active.certificate.request.statement.Cut.Sequence++
			case "clock":
				r.clock.failed = true
			case "elapsed":
				r.active.started.profile[0] ^= 1
			case "expired":
				ticks.Add(authorityRenewalLifetime)
			}
			calls := 0
			sample := r.clock.producer.sample
			r.clock.producer.sample = func() (authorityTimeStamp, error) { calls++; return sample() }
			capture := decisiontrace.Start(16)
			if capture == nil {
				t.Fatal("capture already owned")
			}
			defer capture.Stop()
			r.kernel.gate.Lock()
			_, _, err := r.currentLocked()
			r.kernel.gate.Unlock()
			if err == nil {
				t.Fatal("diagnostic granted refused renewal")
			}
			capture.Stop()
			events, dropped := capture.Drain()
			var observed []decisiontrace.Event
			for _, event := range events {
				if event.Kind == "browser_decision" && event.Stage == "renewal_current" {
					observed = append(observed, event)
				}
			}
			wantCalls := 0
			if reason == "elapsed" || reason == "expired" {
				wantCalls = 1
			}
			if dropped != 0 || len(observed) != 1 || observed[0].Stage != "renewal_current" || observed[0].Reason != reason || observed[0].Object != r.clock.traceID || calls != wantCalls {
				t.Fatal("diagnostic changed first refusal/sample count or clock correlation", observed, dropped, calls)
			}
		})
	}
}

func TestAuthorityRenewalReceiverAbandonAndEpoch(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	clock, _ := fakeAuthorityTimeOwner(t)
	r := &authorityRenewalReceiver{kernel: n.nodes[1], clock: clock, workloads: [32]byte{7}}
	raw, err := r.challenge()
	if err != nil {
		t.Fatal(err)
	}
	r.abandon([]byte("other request"))
	if r.pending == nil {
		t.Fatal("unrelated abandon")
	}
	r.abandon(raw)
	if r.pending != nil {
		t.Fatal("pending retained")
	}
	newRaw, err := r.challenge()
	if err != nil {
		t.Fatal(err)
	}
	if string(newRaw) == string(raw) {
		t.Fatal("challenge reused")
	}
	v, err := n.nodes[2].signAuthorityRenewal(newRaw, r.workloads, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	clock.producer.sample = func() (authorityTimeStamp, error) {
		s := fakeAuthorityTimeStamp(100_000_000)
		s.process[0]++
		return s, nil
	}
	if err = r.receive(newRaw, v); err == nil || r.pending != nil {
		t.Fatal("new process used old challenge")
	}
}

func TestAuthorityRenewalBindingFirstMismatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*authorityRenewalReceiver, *authorityRenewalStatement)
		want authorityRenewalBindingFailure
	}{
		{"match", func(*authorityRenewalReceiver, *authorityRenewalStatement) {}, authorityRenewalBindingNone},
		{"configuration", func(r *authorityRenewalReceiver, _ *authorityRenewalStatement) {
			r.kernel.config.configured = &ConfiguredTrust{}
		}, authorityRenewalBindingConfiguration},
		{"chosen", func(r *authorityRenewalReceiver, _ *authorityRenewalStatement) { r.kernel.chosen = &s2cMessage{} }, authorityRenewalBindingChosen},
		{"slot", func(_ *authorityRenewalReceiver, s *authorityRenewalStatement) { s.Slot++ }, authorityRenewalBindingSlot},
		{"prefix", func(_ *authorityRenewalReceiver, s *authorityRenewalStatement) { s.Prefix[0] ^= 1 }, authorityRenewalBindingPrefix},
		{"cut", func(_ *authorityRenewalReceiver, s *authorityRenewalStatement) { s.Cut.Sequence++ }, authorityRenewalBindingCut},
		{"capsule", func(_ *authorityRenewalReceiver, s *authorityRenewalStatement) { s.Capsule[0] ^= 1 }, authorityRenewalBindingCapsule},
		{"receipt slot", func(r *authorityRenewalReceiver, _ *authorityRenewalStatement) { r.kernel.replayReceipt.ControlSlot++ }, authorityRenewalBindingReceiptSlot},
		{"receipt prefix", func(r *authorityRenewalReceiver, _ *authorityRenewalStatement) {
			r.kernel.replayReceipt.ControlPrefix[0] ^= 1
		}, authorityRenewalBindingReceiptPrefix},
		{"cut before capsule", func(_ *authorityRenewalReceiver, s *authorityRenewalStatement) { s.Cut.Sequence++; s.Capsule[0] ^= 1 }, authorityRenewalBindingCut},
		{"capsule before receipt slot", func(r *authorityRenewalReceiver, s *authorityRenewalStatement) {
			s.Capsule[0] ^= 1
			r.kernel.replayReceipt.ControlSlot++
		}, authorityRenewalBindingCapsule},
		{"receipt slot before prefix", func(r *authorityRenewalReceiver, _ *authorityRenewalStatement) {
			r.kernel.replayReceipt.ControlSlot++
			r.kernel.replayReceipt.ControlPrefix[0] ^= 1
		}, authorityRenewalBindingReceiptSlot},
		{"configuration before nil projection", func(r *authorityRenewalReceiver, _ *authorityRenewalStatement) {
			r.kernel.config.configured = &ConfiguredTrust{}
			r.kernel.chosen = &s2cMessage{}
			r.kernel.replayState.projection = nil
		}, authorityRenewalBindingConfiguration},
		{"chosen before nil projection", func(r *authorityRenewalReceiver, s *authorityRenewalStatement) {
			r.kernel.chosen = &s2cMessage{}
			s.Slot++
			r.kernel.replayState.projection = nil
		}, authorityRenewalBindingChosen},
		{"slot before nil projection", func(r *authorityRenewalReceiver, s *authorityRenewalStatement) {
			s.Slot++
			s.Prefix[0] ^= 1
			r.kernel.replayState.projection = nil
		}, authorityRenewalBindingSlot},
		{"prefix before nil projection", func(r *authorityRenewalReceiver, s *authorityRenewalStatement) {
			s.Prefix[0] ^= 1
			r.kernel.replayState.projection = nil
		}, authorityRenewalBindingPrefix},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _ := authorityTestNativeCluster(t, 3)
			r, _ := authorityTestReceiver(t, n)
			s := r.active.certificate.request.statement
			tc.edit(r, &s)
			r.kernel.gate.Lock()
			defer r.kernel.gate.Unlock()
			state, receipt := r.kernel.replayState, r.kernel.replayReceipt
			legacy := r.kernel.configuredReadyLocked() && r.kernel.chosen == nil && state.slot == s.Slot && state.prefix == s.Prefix && state.projection.cut == s.Cut && receipt.CapsuleDigest == s.Capsule && receipt.ControlSlot == s.Slot && receipt.ControlPrefix == s.Prefix
			if got := r.bindingFailureLocked(s); got != tc.want || r.matchesLocked(s) != legacy || legacy != (tc.want == authorityRenewalBindingNone) {
				t.Fatal("binding changed original truth or first short circuit", got, tc.want, legacy)
			}
		})
	}
}

func TestAuthorityRenewalBindingCallSites(t *testing.T) {
	for _, tc := range []struct {
		site  authorityRenewalCallSite
		stage string
	}{
		{authorityRenewalDefault, "renewal_binding"},
		{authorityRenewalPublicPurposeNotBefore, "renewal_binding_public_purpose_not_before"},
		{authorityRenewalPublicPurposeRead, "renewal_binding_public_purpose_read"},
		{authorityRenewalOriginPurposeBegin, "renewal_binding_origin_purpose_begin"},
		{authorityRenewalOriginPurposeStart, "renewal_binding_origin_purpose_start"},
		{authorityRenewalOriginPurposeComplete, "renewal_binding_origin_purpose_complete"},
		{authorityRenewalNetworkReady, "renewal_binding_network_ready"},
		{authorityRenewalCallSite(255), "renewal_binding"},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			n, _ := authorityTestNativeCluster(t, 3)
			r, _ := authorityTestReceiver(t, n)
			r.clock.traceID = 71
			r.active.certificate.request.statement.Cut.Sequence++
			r.active.certificate.request.canonical = "private-request-and-token"
			r.active.certificate.raw = "private-certificate-body"
			active, pending := r.active, r.pending
			statement := active.certificate.request.statement
			calls := 0
			sample := r.clock.producer.sample
			r.clock.producer.sample = func() (authorityTimeStamp, error) { calls++; return sample() }
			capture := decisiontrace.Start(8)
			if capture == nil {
				t.Fatal("capture already owned")
			}
			defer capture.Stop()
			r.kernel.gate.Lock()
			_, _, err := r.currentLockedAt(tc.site)
			r.kernel.gate.Unlock()
			capture.Stop()
			events, dropped := capture.Drain()
			if err != errAuthorityRenewal || !errors.Is(err, errAuthorityRenewal) || calls != 0 || r.active != active || r.pending != pending || active.certificate.request.statement != statement {
				t.Fatal("diagnostic changed refusal, sample or receiver state", err, calls)
			}
			if dropped != 0 || len(events) != 2 || events[0].Kind != "browser_decision" || events[0].Stage != "renewal_current" || events[0].Reason != "binding" || events[1].Kind != "browser_decision" || events[1].Stage != tc.stage || events[1].Reason != "projection_cut" || events[0].Object != 71 || events[1].Object != 71 {
				t.Fatal("lost same-pass fixed binding detail", events, dropped)
			}
			raw, err := json.Marshal(events)
			if err != nil || strings.Contains(string(raw), "private-request") || strings.Contains(string(raw), "private-certificate") {
				t.Fatal("binding trace exposed private input", err)
			}
		})
	}
}
