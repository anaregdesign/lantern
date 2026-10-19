package security

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"
)

// Real file-backed participants and encoded messages. The scheduler only
// withholds delivery; neither a partition nor restart changes the voter set.
func TestAuthorityEvenVoterPartitionsAndDurableRecovery(t *testing.T) {
	for _, count := range []int{2, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			n := s2cTestNativeCluster(t, count, nil)
			for _, proposer := range []uint32{1, uint32(count)} {
				n.begin(proposer, [32]byte{})
				for id := uint32(1); id <= uint32(count); id++ {
					if (id <= uint32(count/2)) != (proposer <= uint32(count/2)) {
						continue
					}
					n.send(s2cPrepare, proposer, id)
					n.send(s2cPromise, id, proposer)
				}
				if n.nodes[proposer].selected != nil || len(n.nodes[proposer].promises) != count/2 {
					t.Fatal("equal partition selected without a strict majority")
				}
			}
			for id := uint32(1); id <= uint32(count); id++ {
				n.restart(id)
				_, r, err := n.nodes[id].ReadLocalCut()
				if err != nil || r.ControlSlot != 0 || n.nodes[id].accepted != nil {
					t.Fatal("partition or restart fabricated an ACCEPT/commit", id, err)
				}
			}
			// Undelivered traffic from the old partition may be lost. A fresh
			// ballot must recover from durable promises, without queued assistance.
			n.queue = nil
			quorum := make([]uint32, n.fixture.trust.majority())
			for i := range quorum {
				quorum[i] = uint32(i + 1)
			}
			n.selectValue(1, [32]byte{}, quorum...)
			n.choose(1, quorum...)
			for id := uint32(2); id <= uint32(count); id++ {
				n.send(s2cChosen, 1, id)
			}
			for id := uint32(1); id <= uint32(count); id++ {
				n.restart(id)
				_, r, err := n.nodes[id].ReadLocalCut()
				if err != nil || r.ControlSlot != 1 {
					t.Fatal("healed strict majority did not durably drain", id, err)
				}
			}
		})
	}
}

func TestAuthorityEvenVoterOriginalSurvivesHigherBallotAndResume(t *testing.T) {
	for _, count := range []int{2, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			n, keys := authorityTestNativeCluster(t, count)
			h, op := authorityTestHeader(t, n.fixture)
			raw := authorityTestSealHeader(t, n.fixture, keys[1], h, op)
			o := n.nodes[1]
			o.gate.Lock()
			_, err := o.reserveOriginLocked(h.ID, op.digest, s2cCredit{})
			o.gate.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			digest, err := o.persistSealedOriginH(raw)
			if err != nil {
				t.Fatal(err)
			}
			n.restart(1)
			quorum := make([]uint32, n.fixture.trust.majority())
			for i := range quorum {
				quorum[i] = uint32(i + 1)
			}
			n.selectValue(1, digest, quorum...)
			for _, id := range quorum {
				n.send(s2cAccept, 1, id)
				n.restart(id)
				if n.nodes[id].accepted == nil || n.nodes[id].accepted.value != digest {
					t.Fatal("hidden ACCEPT lost at resume", id)
				}
			}
			// A different enrolled proposer requests NOOP. Phase 1 must carry the
			// retained original H, with no refreshed credentials or reauthorization.
			proposer := uint32(count)
			quorum[len(quorum)-1] = proposer
			n.selectValue(proposer, [32]byte{}, quorum...)
			selected := n.nodes[proposer].selected
			if selected == nil || selected.h == nil || selected.value != digest || !bytes.Equal([]byte(selected.h.raw), raw) {
				t.Fatal("higher ballot replaced the original H")
			}
			n.choose(proposer, quorum...)
			n.restart(proposer)
			status, result, _, err := n.nodes[proposer].LookupOriginal(h.ID)
			if err != nil || status != S1Known || result == nil || result.Disposition() != S1Applied || result.HandoffDigest() != digest {
				t.Fatal("resumed result changed original identity", status, err)
			}
			n.restart(1)
			if n.nodes[1].originSerial != h.Serial || n.nodes[1].origins[digest].raw != string(raw) {
				t.Fatal("original bytes or serial changed on retry")
			}
		})
	}
}

func TestAuthorityEvenVoterAllRenewalIntersections(t *testing.T) {
	for _, count := range []int{2, 4} {
		q := count/2 + 1
		var quorums [][]uint32
		for mask := 1; mask < 1<<count; mask++ {
			var members []uint32
			for i := 0; i < count; i++ {
				if mask&(1<<i) != 0 {
					members = append(members, uint32(i+1))
				}
			}
			if len(members) == q {
				quorums = append(quorums, members)
			}
		}
		for _, accepted := range quorums {
			t.Run(fmt.Sprintf("%d/accepted-%v", count, accepted), func(t *testing.T) {
				n := s2cTestNativeCluster(t, count, nil)
				clock, ticks := fakeAuthorityTimeOwner(t)
				r := &authorityRenewalReceiver{kernel: n.nodes[1], clock: clock, workloads: [32]byte{7}}
				raw, err := r.challenge()
				if err != nil {
					t.Fatal(err)
				}
				request, err := parseAuthorityRenewalRequest(raw, n.fixture.trust, r.workloads)
				if err != nil {
					t.Fatal(err)
				}
				before := make(map[uint32][]byte)
				for id := uint32(1); id <= uint32(count); id++ {
					before[id], err = n.nodes[id].signAuthorityRenewal(raw, r.workloads, func() error { return nil })
					if err != nil {
						t.Fatal(err)
					}
				}
				n.selectValue(1, [32]byte{}, accepted...)
				for _, id := range accepted {
					n.send(s2cAccept, 1, id)
					n.restart(id)
				}
				r.kernel = n.nodes[1]
				for _, renewal := range quorums {
					var votes []authorityRenewalVote
					intersections := 0
					for _, id := range renewal {
						_, receipt, err := n.nodes[id].ReadLocalCut()
						if err != nil || receipt.ControlSlot != 0 {
							t.Fatal("hidden ACCEPT was installed", id, err)
						}
						isAccepted := false
						for _, a := range accepted {
							isAccepted = isAccepted || a == id
						}
						v, err := n.nodes[id].signAuthorityRenewal(raw, r.workloads, func() error { return nil })
						if isAccepted {
							intersections++
							if err == nil {
								t.Fatal("durably accepted voter signed the old head", id)
							}
							continue
						}
						if err != nil {
							t.Fatal("unaccepted voter refused its unchanged head", id, err)
						}
						vote, err := parseAuthorityRenewalVote(v, request, n.fixture.trust)
						if err != nil {
							t.Fatal(err)
						}
						votes = append(votes, vote)
					}
					if intersections < 2 || len(votes) >= q {
						t.Fatal("even strict majorities did not intersect", accepted, renewal)
					}
					if _, err := encodeAuthorityRenewalCertificate(raw, votes, n.fixture.trust, r.workloads); err == nil {
						t.Fatal("post-ACCEPT old-head certificate formed", accepted, renewal)
					}
				}
				// Pre-ACCEPT signatures can arrive later. They retain the original
				// challenge start, so receiving them never extends its lifetime.
				ticks.Add(authorityRenewalLifetime - uint64(time.Second))
				for _, id := range quorums[0] {
					if err := r.receive(raw, before[id]); err != nil {
						t.Fatal("delayed original vote", err)
					}
				}
				if r.active == nil {
					t.Fatal("pre-ACCEPT quorum was misrepresented as retroactively revoked")
				}
				ticks.Add(uint64(time.Second))
				r.kernel.gate.Lock()
				_, _, err = r.currentLocked()
				r.kernel.gate.Unlock()
				if err == nil {
					t.Fatal("delayed certificate extended the original challenge")
				}
			})
		}
	}
}

// These are deterministic factual time owners, not configured-source time
// qualification. TLS and both real voting and separate origin keys are used.
func TestAuthorityEvenVoterTLSAvailability(t *testing.T) {
	for _, count := range []int{2, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			n, owners, ticks := authorityTestCompositeMembers(t, count, 1, nil)
			o := owners[1]
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			if err := o.network.renewAuthority(ctx); err != nil {
				t.Fatal("full enrolled set did not renew", err)
			}
			if err := n.nodes[uint32(count)].Close(); err != nil {
				t.Fatal(err)
			}
			if count == 4 {
				if _, err := o.network.Drive(ctx, [32]byte{}); err != nil {
					t.Fatal("three reachable voters did not choose", err)
				}
				authorityTestConverge(t, owners)
				if err := o.network.renewAuthority(ctx); err != nil {
					t.Fatal("three reachable voters did not renew", err)
				}
				if err := n.nodes[3].Close(); err != nil {
					t.Fatal(err)
				}
			}
			_, before, err := o.network.kernel.ReadLocalCut()
			if err != nil {
				t.Fatal(err)
			}
			active := o.network.receiver.active
			short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
			// The call may report the still-valid previous window. Only a new
			// certificate would establish a forbidden minority renewal.
			_ = o.network.renewAuthority(short)
			if o.network.receiver.active != active {
				t.Fatal("half the original voters replaced the renewal certificate")
			}
			stop()
			short, stop = context.WithTimeout(ctx, 300*time.Millisecond)
			if _, err := o.network.Drive(short, [32]byte{}); err == nil {
				t.Fatal("half the original voters chose")
			}
			stop()
			_, after, err := o.network.kernel.ReadLocalCut()
			if err != nil || after != before {
				t.Fatal("minority advanced the drained cut", err)
			}
			ticks[1].Add(authorityRenewalLifetime)
			if (&CurrentAuthority{origin: o}).Ready(ctx) {
				t.Fatal("lost-quorum authority survived its original expiry")
			}
			short, stop = context.WithTimeout(ctx, 300*time.Millisecond)
			if err := o.network.renewAuthority(short); err == nil || o.network.receiver.active != active {
				t.Fatal("expired minority obtained current authority", err)
			}
			stop()
		})
	}
}
