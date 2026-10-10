package security

import "testing"

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
