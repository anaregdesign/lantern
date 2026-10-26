package security

import (
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
				if event.Kind == "browser_decision" {
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
