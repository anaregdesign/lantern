package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestAuthorityOriginReservationIntactRestart(t *testing.T) {
	n, _ := authorityTestNativeCluster(t, 3)
	o := n.nodes[1]
	d, _ := o.trust.origin(1)
	cut := o.replayState.projection.cut
	id := FullChangeID{S1Version, cut.Domain, cut.Cohort, d.Namespace, [16]byte{1}}
	o.gate.Lock()
	r, err := o.reserveOriginLocked(id, [32]byte{9}, s2cCredit{})
	o.gate.Unlock()
	if err != nil || r.Serial != 1 || len(o.pending) != 0 || len(o.origins) != 0 {
		t.Fatal("reservation granted authority or failed", r, err)
	}
	if !bytes.HasPrefix([]byte(o.p.records[2]), []byte(authorityPMagic)) {
		t.Fatal("reservation used historical grammar")
	}
	n.restart(1)
	o = n.nodes[1]
	if o.originSerial != 1 || o.originReservations[1] != r || len(o.pending) != 0 || len(o.serials) != 0 {
		t.Fatal("bare reservation not recovered as bookkeeping")
	}
	o.gate.Lock()
	_, err = o.reserveOriginLocked(id, [32]byte{9}, s2cCredit{})
	id.Nonce[0]++
	next, nextErr := o.reserveOriginLocked(id, [32]byte{10}, s2cCredit{})
	o.gate.Unlock()
	if err == nil || nextErr != nil || next.Serial != 2 {
		t.Fatal("reused ambiguous ID or serial hole", err, next, nextErr)
	}
	// A pending reservation alone is not a hidden consensus choice.
	if _, err = o.signAuthorityRenewal(testAuthorityRenewalRequest(t, n, 1, [32]byte{8}), [32]byte{8}, func() error { return nil }); err != nil {
		t.Fatal("reservation blocked current prefix", err)
	}
}

func TestAuthorityOriginReservationFailureAndCapacity(t *testing.T) {
	for _, phase := range []string{"before WAL", "after durable WAL"} {
		t.Run(phase, func(t *testing.T) {
			n, _ := authorityTestNativeCluster(t, 3)
			o := n.nodes[1]
			cut := o.replayState.projection.cut
			d, _ := o.trust.origin(1)
			id := FullChangeID{S1Version, cut.Domain, cut.Cohort, d.Namespace, [16]byte{1}}
			floorP, floorB, err := o.Floors()
			if err != nil {
				t.Fatal(err)
			}
			boom := func() { panic("reservation interruption") }
			if phase == "before WAL" {
				o.p.hooks = &s2cJournalHooks{beforeAppend: boom}
			} else {
				o.p.hooks = &s2cJournalHooks{afterLog: boom}
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Error("checkpoint did not interrupt")
					}
				}()
				o.gate.Lock()
				defer o.gate.Unlock()
				defer o.poisonPanic()
				_, _ = o.reserveOriginLocked(id, [32]byte{2}, s2cCredit{})
			}()
			if !errors.Is(o.readyLocked(), errS2CUnknown) {
				t.Fatal("uncertain writer remained open")
			}
			n.stop(1)
			o, err = resumeS2CParticipant(n.configs[1], floorP, floorB)
			if err != nil {
				t.Fatal(err)
			}
			n.nodes[1] = o
			want := uint64(0)
			if phase == "after durable WAL" {
				want = 1
			}
			if o.originSerial != want || len(o.pending) != 0 {
				t.Fatal("native replay did not retain exact reservation", o.originSerial)
			}
			id.Nonce[0]++
			o.gate.Lock()
			r, err := o.reserveOriginLocked(id, [32]byte{3}, s2cCredit{})
			o.gate.Unlock()
			if err != nil || r.Serial != want+1 {
				t.Fatal(r, err)
			}
		})
	}
	t.Run("protected completion", func(t *testing.T) {
		n, _ := authorityTestNativeCluster(t, 3)
		o := n.nodes[1]
		cut := o.replayState.projection.cut
		d, _ := o.trust.origin(1)
		id := FullChangeID{S1Version, cut.Domain, cut.Cohort, d.Namespace, [16]byte{1}}
		before := o.p.floor
		o.credit.PRecords = o.p.policy.Records - o.p.count
		o.gate.Lock()
		_, err := o.reserveOriginLocked(id, [32]byte{2}, s2cCredit{})
		o.gate.Unlock()
		if !errors.Is(err, errS2CJournalCapacity) || o.p.floor != before || o.originSerial != 0 || o.unknown != nil {
			t.Fatal("reservation stole completion credit", err)
		}
	})
}

func TestAuthorityOriginReservationCanonicalBinding(t *testing.T) {
	n, _ := authorityTestNativeCluster(t, 3)
	o := n.nodes[1]
	cut := o.replayState.projection.cut
	d, _ := o.trust.origin(1)
	r := authorityOriginReservation{authorityAdmissionVersion, o.trust.scope, 1, o.trust.originDigests[1], d.Incarnation, 1,
		FullChangeID{S1Version, cut.Domain, cut.Cohort, d.Namespace, [16]byte{1}}, [32]byte{9}}
	for name, edit := range map[string]func(*authorityOriginReservation){
		"version":     func(r *authorityOriginReservation) { r.Version = 1 },
		"scope":       func(r *authorityOriginReservation) { r.Scope[0] ^= 1 },
		"origin":      func(r *authorityOriginReservation) { r.Origin++ },
		"descriptor":  func(r *authorityOriginReservation) { r.Descriptor[0] ^= 1 },
		"incarnation": func(r *authorityOriginReservation) { r.Incarnation[0] ^= 1 },
		"serial":      func(r *authorityOriginReservation) { r.Serial++ },
		"domain":      func(r *authorityOriginReservation) { r.ID.Domain[0] ^= 1 },
		"cohort":      func(r *authorityOriginReservation) { r.ID.Cohort[0] ^= 1 },
		"namespace":   func(r *authorityOriginReservation) { r.ID.Namespace++ },
		"nonce":       func(r *authorityOriginReservation) { r.ID.Nonce = [16]byte{} },
		"operation":   func(r *authorityOriginReservation) { r.Operation = [32]byte{} },
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			edit(&bad)
			raw, _ := json.Marshal(bad)
			if o.replayReservation(string(raw)) == nil {
				t.Fatal("invalid reservation accepted")
			}
		})
	}
	raw, _ := json.Marshal(r)
	if o.replayReservation(string(raw)+" ") == nil {
		t.Fatal("noncanonical reservation")
	}
	if err := o.replayReservation(string(raw)); err != nil {
		t.Fatal(err)
	}
	if o.replayReservation(string(raw)) == nil {
		t.Fatal("duplicate reservation")
	}
}
