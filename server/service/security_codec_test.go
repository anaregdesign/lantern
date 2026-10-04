package service

import (
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestSecurityCodecStrictContract(t *testing.T) {
	role := &pb.SecurityRole{Id: "reader", Rules: []*pb.SecurityRule{{Id: "read", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:"}}}}
	decoded, err := decodeSecurityRole(role)
	if err != nil || *decoded.Rules[0].Prefix != "orders:" || decoded.Rules[0].Action != security.VertexRead {
		t.Fatal(decoded, err)
	}
	if encodeSecurityRole(decoded).Rules[0].GetPrefix() != "orders:" {
		t.Fatal("logical prefix changed")
	}
	for _, bad := range []*pb.SecurityRule{
		{Id: "bad", Effect: pb.SecurityEffect(99), Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{}},
		{Id: "bad", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction(99), Resource: &pb.SecurityRule_Prefix{}},
		{Id: "bad", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_MANAGE, Resource: &pb.SecurityRule_Global{Global: false}},
	} {
		role.Rules = []*pb.SecurityRule{bad}
		if _, err := decodeSecurityRole(role); err == nil {
			t.Fatal("unknown or malformed rule accepted")
		}
	}
	change := &pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "reader"}}}
	nested := change.GetPutRole()
	nested.ProtoReflect().SetUnknown(protowire.AppendTag(nil, 999, protowire.VarintType))
	nested.ProtoReflect().SetUnknown(append(nested.ProtoReflect().GetUnknown(), 1))
	if strictSecurityMessage(change) == nil {
		t.Fatal("unknown direct-grant field ignored")
	}
	issuer := encodeSecurityIssuer(security.Issuer{URL: "https://idp.example", SecretRef: "private_binding", ConfigRevision: 3})
	if issuer.SecretRef != nil || issuer.ConfigRevision != 3 {
		t.Fatal("secret handle leaked")
	}
	if securityErrorCode(security.ErrLastAdministrator) != connect.CodeFailedPrecondition {
		t.Fatal("last admin error mapping")
	}
}

func TestSecurityCodecRejectsObsoleteEdgeGrantsAndPairSelectors(t *testing.T) {
	for _, action := range []pb.SecurityAction{
		pb.SecurityAction_SECURITY_ACTION_VERTEX_READ,
		pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ,
		pb.SecurityAction_SECURITY_ACTION_EDGE_READ,
		pb.SecurityAction_SECURITY_ACTION_EDGE_CREATE,
	} {
		for _, resource := range []*pb.SecurityRule_Pair{
			{}, {Pair: &pb.SecurityPrefixPair{TailPrefix: "users:", HeadPrefix: "targets:"}},
		} {
			role := &pb.SecurityRole{Id: "obsolete", Rules: []*pb.SecurityRule{{Id: "rule", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: action, Resource: resource}}}
			if _, err := decodeSecurityRole(role); err == nil {
				t.Fatal("obsolete pair decoded", action, resource)
			}
		}
	}
	for _, action := range []pb.SecurityAction{
		pb.SecurityAction_SECURITY_ACTION_EDGE_READ, pb.SecurityAction_SECURITY_ACTION_EDGE_CREATE,
		pb.SecurityAction_SECURITY_ACTION_EDGE_ADD, pb.SecurityAction_SECURITY_ACTION_EDGE_WRITE,
		pb.SecurityAction_SECURITY_ACTION_EDGE_DELETE,
	} {
		role := &pb.SecurityRole{Id: "obsolete", Rules: []*pb.SecurityRule{{Id: "rule", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: action, Resource: &pb.SecurityRule_Prefix{Prefix: ""}}}}
		if _, err := decodeSecurityRole(role); err == nil {
			t.Fatal("obsolete Edge grant decoded", action)
		}
	}
}

func TestSecurityRoleReadMetadataCannotBecomeAWrite(t *testing.T) {
	role := security.Role{ID: "reader", Name: "Reader"}
	image := security.Image{Principals: []security.Principal{{Assignments: []security.RoleAssignment{{RoleID: "reader", EnvOwned: true}}}}}
	locked := encodeSecurityManagedRole(role, image)
	if !locked.GetEnvOwned() {
		t.Fatal("environment policy lock missing")
	}
	if _, err := decodeSecurityRole(locked); err == nil {
		t.Fatal("read metadata accepted as client grant metadata")
	}
	locked.EnvOwned = false
	if _, err := decodeSecurityRole(locked); err != nil {
		t.Fatal(err)
	}
	if encodeSecurityManagedRole(role, security.Image{}).GetEnvOwned() {
		t.Fatal("ordinary Role marked environment-owned")
	}
}
