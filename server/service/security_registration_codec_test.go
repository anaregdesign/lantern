package service

import (
	"reflect"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func TestRegistrationPolicyCodecClosedShape(t *testing.T) {
	wire := &pb.SecurityRegistrationPolicy{Version: 1, Enabled: true, RoleId: security.PersonalUserRoleID, Profiles: []*pb.SecurityRegistrationProfile{{Issuer: security.GoogleIssuer, ClientProfileId: "android"}}}
	policy, err := decodeRegistrationPolicy(wire)
	if err != nil || !proto.Equal(wire, encodeRegistrationPolicy(policy)) {
		t.Fatal("policy round trip", err)
	}
	change, err := decodeSecurityChange(&pb.SecurityChange{Operation: &pb.SecurityChange_PutRegistrationPolicy{PutRegistrationPolicy: wire}})
	if err != nil || change.Kind != security.PutRegistrationPolicy || !reflect.DeepEqual(change.RegistrationPolicy, policy) {
		t.Fatal("policy change codec", err)
	}
	for name, edit := range map[string]func(*pb.SecurityRegistrationPolicy){
		"unknown version":   func(p *pb.SecurityRegistrationPolicy) { p.Version = 2 },
		"arbitrary role":    func(p *pb.SecurityRegistrationPolicy) { p.RoleId = "security_admin" },
		"no exact profiles": func(p *pb.SecurityRegistrationPolicy) { p.Profiles = nil },
		"duplicate profile": func(p *pb.SecurityRegistrationPolicy) {
			p.Profiles = append(p.Profiles, proto.Clone(p.Profiles[0]).(*pb.SecurityRegistrationProfile))
		},
		"nil profile":     func(p *pb.SecurityRegistrationPolicy) { p.Profiles[0] = nil },
		"insecure issuer": func(p *pb.SecurityRegistrationPolicy) { p.Profiles[0].Issuer = "http://accounts.google.com" },
		"unknown field":   func(p *pb.SecurityRegistrationPolicy) { p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
	} {
		t.Run(name, func(t *testing.T) {
			p := proto.Clone(wire).(*pb.SecurityRegistrationPolicy)
			edit(p)
			if _, err := decodeRegistrationPolicy(p); err == nil {
				t.Fatal("invalid policy decoded")
			}
		})
	}
	if encodeRegistrationPolicy(nil) != nil {
		t.Fatal("absent policy silently enabled")
	}
	if _, err := decodeRegistrationPolicy(nil); err == nil {
		t.Fatal("absent mutation accepted")
	}
}

func TestRegistrationReviewCodecRequiresExactContract(t *testing.T) {
	h, _, _ := securityAPIFixture(t)
	revision, _ := h.store.Current()
	p, err := security.NewS1Projection(revision.Snapshot().Image(), security.DefaultPolicyLimits(), [32]byte{1}, [32]byte{2}, [32]byte{3}, [16]byte{4})
	if err != nil {
		t.Fatal(err)
	}
	actor := security.Identity{Kind: security.OIDCPrincipal, Issuer: security.GoogleIssuer, Subject: "new-user"}
	command := security.S1Command{Kind: security.S1RegisterPrincipal, Registration: &security.PrincipalRegistration{UserUID: "11111111111111111111111111111111", ClientProfileID: "android", IssuerConfigRevision: 1}}
	op, err := security.NewS1Operation(actor, p.Cut(), command, security.DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	review := security.CurrentReview{Profile: security.CurrentProfile{Version: security.CurrentPublicVersion, Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{4}, Protocol: [32]byte{5}, Time: [32]byte{6}, Membership: [32]byte{7}, Configuration: [32]byte{8}},
		ID: security.FullChangeID{Version: security.S1Version, Domain: [32]byte{1}, Cohort: [32]byte{2}, Namespace: 1, Nonce: [16]byte{9}}, Operation: op}
	wire, err := encodeRegistrationReview(review)
	if err != nil || wire.UserId != command.Registration.UserUID || wire.ClientProfileId != "android" {
		t.Fatal("review encoding", err)
	}
	if _, err := decodeRegistrationReview(nil, wire); err == nil {
		t.Fatal("decode without current owner")
	}
	if _, err := decodeRegistrationReview(&security.CurrentAuthority{}, wire); err == nil {
		t.Fatal("uninstalled owner decoded authority review")
	}
	if _, err := encodeRegistrationReview(security.CurrentReview{}); err == nil {
		t.Fatal("empty review encoded")
	}
	changed := review
	changed.Profile.Generation[0] ^= 1
	if _, err := encodeRegistrationReview(changed); err == nil {
		t.Fatal("review profile/generation mismatch encoded")
	}
}
