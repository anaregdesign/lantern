package security

import (
	"reflect"
	"strings"
	"testing"
)

func TestS1TwoAdminRemovalsCannotAdmitAdminlessUnion(t *testing.T) {
	image := s1Image()
	image.Principals[1].Assignments = []RoleAssignment{{RoleID: "security_admin"}}
	base := s1Fixture(t, image)
	admin, bob := testIdentity(), s1Bob()
	left := s1Seal(base.projection, s1Operation(t, base.projection, admin, s1Changes(Change{Kind: DeleteAssignment, Identity: &bob, RoleID: "security_admin"})), 1, false)
	right := s1Seal(base.projection, s1Operation(t, base.projection, bob, s1Changes(Change{Kind: DeleteAssignment, Identity: &admin, RoleID: "security_admin"})), 2, false)
	for _, order := range [][]*S1Handoff{{left, right}, {right, left}} {
		first := s1Apply(t, base, order[0])
		second := s1Apply(t, first.State, order[1])
		if first.Outcome.Disposition() != S1Applied || second.Outcome.Disposition() != S1RejectedCAS {
			t.Fatal("concurrent admin union admitted")
		}
		count := 0
		for id := range second.State.projection.snapshot.principals {
			a, active := second.State.projection.snapshot.AccessFor(id)
			if active && second.State.projection.snapshot.HumanIdentity(id) && a.AllowsGlobal(SecurityManage) {
				count++
			}
		}
		if count != 1 {
			t.Fatal("qualified administrator invariant", count)
		}
	}
}

func TestS1SessionApplyOriginalLineageAndCodeProvenance(t *testing.T) {
	base := s1Fixture(t, s1Image())
	actor := testIdentity()
	session := testSession()
	session.CSRFDigest = strings.Repeat("b", 64)
	command := S1Command{Kind: S1IssueSession, Session: &session, SessionLineage: 1}
	op := s1Operation(t, base.projection, actor, command)
	h := s1Seal(base.projection, op, 1, false)
	if r := s1Apply(t, base, h); r.Outcome.Disposition() != S1RejectedAuthority {
		t.Fatal("Bearer minted browser session")
	}
	h = s1Seal(base.projection, op, 2, false)
	h.authorization.authentication.Provenance = BrowserCode
	issued := s1Apply(t, base, h)
	if issued.Outcome.Disposition() != S1Applied {
		t.Fatal("verified Code issuance refused")
	}
	revoke := s1Seal(issued.State.projection, s1Operation(t, issued.State.projection, actor, s1Changes(Change{Kind: RevokeSessions, Identity: &actor})), 3, false)
	revoked := s1Apply(t, issued.State, revoke)
	if revoked.Outcome.Disposition() != S1Applied || revoked.State.projection.SessionLineage(actor) != 2 {
		t.Fatal("revoke-all lineage")
	}
	replay := s1Apply(t, revoked.State, h)
	if replay.Status != S1Replay || replay.Outcome != issued.Outcome {
		t.Fatal("historical session result changed")
	}
	stored, _ := replay.State.projection.snapshot.Session(session.Digest)
	if !stored.Revoked {
		t.Fatal("historical result revived current session")
	}
	session.Digest = strings.Repeat("c", 64)
	command.Session = &session
	old := s1Seal(revoked.State.projection, s1Operation(t, revoked.State.projection, actor, command), 4, false)
	old.authorization.authentication.Provenance = BrowserCode
	if r := s1Apply(t, revoked.State, old); r.Outcome.Disposition() != S1RejectedAuthority {
		t.Fatal("new review silently refreshed callback lineage")
	}
	command.SessionLineage = 2
	fresh := s1Seal(revoked.State.projection, s1Operation(t, revoked.State.projection, actor, command), 5, false)
	fresh.authorization.authentication.Provenance = BrowserCode
	if r := s1Apply(t, revoked.State, fresh); r.Outcome.Disposition() != S1Applied {
		t.Fatal("new lineage login refused")
	}
}

func TestS1GrantRevokeOrderAndHistoricalResult(t *testing.T) {
	image := s1Image()
	image.Principals[1].Assignments = []RoleAssignment{{RoleID: "security_admin"}}
	base := s1Fixture(t, image)
	bob := s1Bob()
	grant := s1Seal(base.projection, s1Operation(t, base.projection, bob, s1Changes(s1ReaderRole())), 1, false)
	revoke := s1Seal(base.projection, s1Operation(t, base.projection, testIdentity(), s1Changes(Change{Kind: DeleteAssignment, Identity: &bob, RoleID: "security_admin"})), 2, false)
	rfirst := s1Apply(t, base, revoke)
	gafter := s1Apply(t, rfirst.State, grant)
	if rfirst.Outcome.Disposition() != S1Applied || gafter.Outcome.Disposition() != S1RejectedCAS {
		t.Fatal("stale grant survived successful revoke")
	}
	gfirst := s1Apply(t, base, grant)
	rafter := s1Apply(t, gfirst.State, revoke)
	if gfirst.Outcome.Disposition() != S1Applied || rafter.Outcome.Disposition() != S1RejectedCAS {
		t.Fatal("revoke observation rewritten from control order")
	}
	fresh := s1Seal(gfirst.State.projection, s1Operation(t, gfirst.State.projection, testIdentity(), s1Changes(Change{Kind: DeleteAssignment, Identity: &bob, RoleID: "security_admin"})), 3, false)
	current := s1Apply(t, gfirst.State, fresh)
	access, _ := current.State.projection.snapshot.AccessFor(bob)
	if access.AllowsGlobal(SecurityManage) {
		t.Fatal("current revocation missing")
	}
	status, outcome := current.State.Lookup(grant.id)
	if status != S1Known || outcome != gfirst.Outcome || outcome.Disposition() != S1Applied || outcome.Observed() != base.projection.cut {
		t.Fatal("history/current authority conflated")
	}
	if retry := s1Apply(t, current.State, grant); retry.Status != S1Replay || retry.Outcome != gfirst.Outcome {
		t.Fatal("historical replay reauthorized")
	}
}

func TestS1BoundedOriginPermutations(t *testing.T) {
	base := s1Fixture(t, s1Image())
	op := s1Operation(t, base.projection, testIdentity(), s1Changes(s1ReaderRole()))
	handoffs := make([]*S1Handoff, 6)
	for i := range handoffs {
		handoffs[i] = s1Seal(base.projection, op, 1, i%2 == 0)
		handoffs[i].origin = [32]byte{byte(20 + i)}
		handoffs[i].serial = uint64(i + 1)
	}
	orders, count := []int{0, 1, 2, 3, 4, 5}, 0
	var visit func(int)
	visit = func(at int) {
		if at < len(orders) {
			for i := at; i < len(orders); i++ {
				orders[at], orders[i] = orders[i], orders[at]
				visit(at + 1)
				orders[at], orders[i] = orders[i], orders[at]
			}
			return
		}
		count++
		s := base
		var first *OriginalOutcome
		for _, i := range orders {
			result := s1Apply(t, s, handoffs[i])
			if first == nil {
				first = result.Outcome
				if result.Status != S1Original || first.Disposition() != S1Applied {
					t.Fatal("first")
				}
			} else if result.Status != S1Replay || result.Outcome != first {
				t.Fatal("duplicate effect")
			}
			s = result.State
		}
		if len(s.ledger) != 1 || s.projection.cut.Sequence != 2 || s.slot != 6 || first.HandoffDigest() != handoffs[orders[0]].digest() {
			t.Fatal("variant changed business/original identity")
		}
	}
	visit(0)
	if count != 720 {
		t.Fatal(count)
	}
}

func TestS1CertifiedDeliveryPermutations(t *testing.T) {
	base := s1Fixture(t, s1Image())
	op := s1Operation(t, base.projection, testIdentity(), s1Changes(s1ReaderRole()))
	h := s1Seal(base.projection, op, 1, false)
	states := []*S1ApplyState{base}
	events := make([]S1CertifiedNext, 4)
	for i := range events {
		var chosen *S1Handoff
		if i != 1 {
			chosen = h
		}
		events[i] = s1Next(states[i], chosen)
		result, err := ApplyS1(states[i], events[i])
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, result.State)
	}
	orders := []int{0, 1, 2, 3}
	count := 0
	var visit func(int)
	visit = func(at int) {
		if at < len(orders) {
			for i := at; i < len(orders); i++ {
				orders[at], orders[i] = orders[i], orders[at]
				visit(at + 1)
				orders[at], orders[i] = orders[i], orders[at]
			}
			return
		}
		count++
		s := base
		pending := map[int]S1CertifiedNext{}
		for _, i := range orders {
			pending[i] = events[i]
			for {
				e, exists := pending[int(s.slot)]
				if !exists {
					break
				}
				r, err := ApplyS1(s, e)
				if err != nil {
					t.Fatal(err)
				}
				delete(pending, int(s.slot))
				s = r.State
			}
		}
		if s.projection.cut != states[4].projection.cut || s.prefix != states[4].prefix || s.slot != states[4].slot || !reflect.DeepEqual(s.projection.Image(), states[4].projection.Image()) || !reflect.DeepEqual(s.ledger, states[4].ledger) {
			t.Fatal("delivery changed certified projection/result")
		}
	}
	visit(0)
	if count != 24 {
		t.Fatal(count)
	}
}
