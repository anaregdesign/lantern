package service

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func TestCurrentSecurityCodecPreservesFullProfileAndCut(t *testing.T) {
	p := security.CurrentProfile{Version: 2, Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{3}, Protocol: [32]byte{4}, Time: [32]byte{5}, Membership: [32]byte{6}, Configuration: [32]byte{7}}
	wire := EncodeCurrentProfile(p)
	decoded, err := decodeCurrentProfile(wire)
	if err != nil || decoded != p {
		t.Fatal("full profile round trip", err)
	}
	for _, edit := range []func(*pb.CurrentAuthorityProfile){func(p *pb.CurrentAuthorityProfile) { p.Version = 1 }, func(p *pb.CurrentAuthorityProfile) { p.Cohort = p.Cohort[:31] }, func(p *pb.CurrentAuthorityProfile) { p.Membership = append(p.Membership, 0) }, func(p *pb.CurrentAuthorityProfile) { p.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01}) }} {
		bad := proto.Clone(wire).(*pb.CurrentAuthorityProfile)
		edit(bad)
		if _, err := decodeCurrentProfile(bad); err == nil {
			t.Fatal("malformed/unknown profile accepted")
		}
	}
	c := security.SemanticCut{Version: 1, Domain: p.Domain, Cohort: p.Cohort, Generation: p.Generation, Sequence: 9, Previous: [32]byte{10}, Projection: [32]byte{11}, Frontier: [32]byte{12}, Fences: [32]byte{13}, Policy: [32]byte{14}}
	cut, err := decodeCurrentCut(encodeCurrentCut(c))
	if err != nil || cut != c {
		t.Fatal("full cut round trip", err)
	}
	badCut := encodeCurrentCut(c)
	badCut.Version = 65537
	if _, err := decodeCurrentCut(badCut); err == nil {
		t.Fatal("version truncation")
	}
	id := security.FullChangeID{Version: 1, Domain: p.Domain, Cohort: p.Cohort, Namespace: 55, Nonce: [16]byte{22}}
	id2, err := decodeCurrentID(encodeCurrentID(id))
	if err != nil || id2 != id {
		t.Fatal("full ID round trip", err)
	}
	if _, err := decodeCurrentID(&pb.CurrentSecurityChangeID{Version: 1, Nonce: make([]byte, 16)}); err == nil {
		t.Fatal("legacy nonce became full ID")
	}
	for _, stage := range []security.CurrentProgress{security.CurrentUnresolved, security.CurrentOriginDurable, security.CurrentChosen, security.CurrentApplied} {
		result := EncodeCurrentResult(security.CurrentChangeResult{Profile: p, ID: id, Intent: [32]byte{88}, Progress: stage})
		if result.Progress == pb.CurrentSecurityProgress_CURRENT_SECURITY_PROGRESS_UNSPECIFIED || result.Original != nil || result.StopObservation != pb.CurrentAuthorizationStopObservation_CURRENT_AUTHORIZATION_STOP_OBSERVATION_NOT_OBSERVED {
			t.Fatal("stage manufactured original or enforcement")
		}
	}
}
