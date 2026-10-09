package security

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// Three real M/P/B/TLS owners. Every hidden-choice/renewal majority pair is
// checked, plus each locally indistinguishable single-accept history. Facts and
// time are explicit unit fixtures; origin signing and persistence are real.
func TestAuthorityRenewalAllMajorityIntersectionsTLS(t *testing.T) {
	quorums := [][2]uint32{{1, 2}, {1, 3}, {2, 3}}
	for _, accepted := range [][]uint32{{1, 2}, {1, 3}, {2, 3}, {1}, {2}, {3}} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			n, owners, _ := authorityTestComposite(t)
			origin := owners[1]
			ctx := s3aTestContext(t)
			if err := origin.network.renewAuthority(ctx); err != nil {
				t.Fatal(err)
			}
			r, err := origin.prepare(ctx, authorityFakeCredentialProducer{}, n.f.genesis.state.projection.cut, s1Changes(s1ReaderRole()))
			if err != nil {
				t.Fatal(err)
			}
			h, err := origin.consume(ctx, r, authorityFakeCredentialProducer{}, [32]byte{})
			if err != nil {
				t.Fatal(err)
			}
			selected := func(id uint32) bool {
				for _, a := range accepted {
					if a == id {
						return true
					}
				}
				return false
			}
			var recovering atomic.Bool
			for _, o := range n.nodes {
				o.hooks.beforeSend = func(_ context.Context, to uint32, raw []byte) error {
					kind := raw[len(s2cMessageMagic)]
					if !recovering.Load() && (kind == s2cAccepted || (kind == s2cAccept && !selected(to))) {
						return errS3AWire
					}
					return nil
				}
			}
			if err := origin.network.Begin(ctx, h.digest); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				ready := true
				for id, o := range n.nodes {
					o.kernel.gate.Lock()
					present := o.kernel.accepted != nil
					slot := o.kernel.replayState.slot
					o.kernel.gate.Unlock()
					if slot != 0 {
						t.Fatal("hidden choice became installed")
					}
					if present != selected(id) {
						ready = false
					}
				}
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("hidden ACCEPT set not retained")
				}
				time.Sleep(5 * time.Millisecond)
			}
			// A materialized-head-only rule would authorize every old-head quorum.
			for _, q := range quorums {
				request, err := origin.network.receiver.challenge()
				if err != nil {
					t.Fatal(err)
				}
				votes := 0
				headOnlyVotes := 0
				for _, id := range q {
					_, receipt, err := n.nodes[id].kernel.ReadLocalCut()
					if err != nil {
						t.Fatal(err)
					}
					if receipt.ControlSlot == 0 {
						headOnlyVotes++
					}
					var raw []byte
					if id == 1 {
						raw, err = origin.network.renewalVote(ctx, 1, request, func() error { return origin.network.check(ctx) })
					} else {
						var status int
						raw, status, err = origin.network.request(ctx, id, authorityRenewalPath, request, 68)
						if status != http.StatusOK {
							err = errAuthorityRenewal
						}
					}
					if err == nil {
						if err := origin.network.receiver.receive(request, raw); err != nil {
							t.Fatal(err)
						}
						votes++
					}
				}
				origin.network.receiver.abandon(request)
				want := 2
				for _, id := range q {
					if selected(id) {
						want--
					}
				}
				if headOnlyVotes != 2 || votes != want || (len(accepted) == 2 && votes >= 2) {
					t.Fatalf("accepted=%v renewal=%v materialized-only=%d proper=%d want=%d", accepted, q, headOnlyVotes, votes, want)
				}
				t.Logf("hidden accepts=%v renewal majority=%v old materialized votes=%d serialized votes=%d", accepted, q, headOnlyVotes, votes)
			}
			recovering.Store(true)
			if err := origin.network.Close(); err != nil {
				t.Fatal(err)
			}
			relay := owners[2]
			original, err := relay.carryOriginal(ctx, []byte(h.raw))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := relay.network.Drive(ctx, original.digest); err != nil {
				t.Fatal("real different-proposer phase-one recovery", err)
			}
			authorityTestConverge(t, owners)
			if err := relay.network.renewAuthority(ctx); err != nil {
				t.Fatal("new drained prefix did not renew", err)
			}
			_, outcome, _, err := relay.network.kernel.LookupOriginal(r.id)
			if err != nil || outcome == nil || outcome.disposition != S1Applied || outcome.handoff != h.digest {
				t.Fatal("recovery replaced original H", err)
			}

		})
	}
}
