package securitytransport

import (
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestSecurityNativeClientSecretReadAndUpdateBoundary(t *testing.T) {
	prior := security.Issuer{URL: security.AppleIssuer, Enabled: true, ConfigRevision: 3, Algorithms: []string{"RS256"}, NativeClients: []security.NativeClient{{ID: "ios", Kind: security.AppleNative, ClientID: "com.example.app", ServiceOrigin: "https://lantern.example", SecretRef: "private-operator-binding"}}}
	read := encodeSecurityIssuer(prior)
	if len(read.NativeClients) != 1 || !read.NativeClients[0].HasSecretBinding || read.NativeClients[0].SecretRef != nil {
		t.Fatal("native secret binding leaked")
	}
	read.ConfigRevision = 0
	read.NativeClients[0].HasSecretBinding = false
	change, err := decodeSecurityChange(&pb.SecurityChange{Operation: &pb.SecurityChange_PutIssuer{PutIssuer: read}})
	if err != nil {
		t.Fatal(err)
	}
	resolved := change.ResolveIssuerSecrets(prior)
	if resolved.NativeClients[0].SecretRef != "private-operator-binding" || security.ValidateIssuerConfiguration(resolved) != nil {
		t.Fatal("omitted exact binding not preserved")
	}
	change.Issuer.NativeClients[0].ClientID = "com.example.other"
	if change.ResolveIssuerSecrets(prior).NativeClients[0].SecretRef != "" {
		t.Fatal("secret rebound to another client")
	}
	read.NativeClients[0].Kind = pb.NativeClientKind(999)
	if _, err := decodeSecurityIssuer(read); err == nil {
		t.Fatal("unknown native profile accepted")
	}
}

func TestSecurityCodecStrictContract(t *testing.T) {
	role := &pb.SecurityRole{Id: "reader", Rules: []*pb.SecurityRule{{Id: "read", Permission: pb.SecurityPermission_SECURITY_PERMISSION_READ, ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_DATA, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:"}}}}
	decoded, err := decodeSecurityRole(role)
	if err != nil || *decoded.Rules[0].Prefix != "orders:" || decoded.Rules[0].Permission != security.Read || !proto.Equal(role, encodeSecurityRole(decoded)) {
		t.Fatal(decoded, err)
	}
	for _, bad := range []*pb.SecurityRule{
		{Id: "bad", Permission: pb.SecurityPermission(99), ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_DATA, Resource: &pb.SecurityRule_Prefix{}},
		{Id: "bad", Permission: pb.SecurityPermission_SECURITY_PERMISSION_READ, ResourceKind: pb.SecurityResource(99)},
		{Id: "bad", Permission: pb.SecurityPermission_SECURITY_PERMISSION_READ, ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_DATA},
		{Id: "bad", Permission: pb.SecurityPermission_SECURITY_PERMISSION_READ, ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_SECURITY, Resource: &pb.SecurityRule_Prefix{}},
		{Id: "bad", Permission: pb.SecurityPermission_SECURITY_PERMISSION_WRITE, ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_OPERATIONS},
		{Id: "bad", Permission: pb.SecurityPermission_SECURITY_PERMISSION_WRITE, ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_SCHEMA},
	} {
		role.Rules = []*pb.SecurityRule{bad}
		if _, err := decodeSecurityRole(role); err == nil {
			t.Fatal("unknown or malformed rule accepted")
		}
	}
	change := &pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "reader"}}}
	nested := change.GetPutRole()
	nested.ProtoReflect().SetUnknown(append(protowire.AppendTag(nil, 999, protowire.VarintType), 1))
	if strictSecurityMessage(change) == nil {
		t.Fatal("unknown direct grant field ignored")
	}
	issuer := encodeSecurityIssuer(security.Issuer{URL: "https://idp.example", SecretRef: "private_binding", ConfigRevision: 3})
	if issuer.SecretRef != nil || issuer.ConfigRevision != 3 {
		t.Fatal("secret handle leaked")
	}
	if securityErrorCode(security.ErrLastAdministrator) != connect.CodeFailedPrecondition {
		t.Fatal("last admin error mapping")
	}
}

func TestSecurityCodecRejectsRetiredSelectors(t *testing.T) {
	// Old global and pair tags are no longer part of the active wire schema.
	// Strict decoding rejects these bytes rather than broadening the new grant.
	for _, tag := range []protowire.Number{2, 3, 5, 6} {
		rule := &pb.SecurityRule{Id: "old", Permission: pb.SecurityPermission_SECURITY_PERMISSION_READ, ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_SECURITY}
		rule.ProtoReflect().SetUnknown(append(protowire.AppendTag(nil, tag, protowire.BytesType), 0))
		if _, err := decodeSecurityRole(&pb.SecurityRole{Id: "old", Rules: []*pb.SecurityRule{rule}}); err == nil {
			t.Fatal("retired selector accepted", tag)
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

func TestSecurityCodecUIDSelector(t *testing.T) {
	role := &pb.SecurityRole{Id: "personal", Rules: []*pb.SecurityRule{{Id: "read", Permission: pb.SecurityPermission_SECURITY_PERMISSION_READ, ResourceKind: pb.SecurityResource_SECURITY_RESOURCE_DATA, Resource: &pb.SecurityRule_PrincipalUidPrefix{PrincipalUidPrefix: &pb.SecurityPrincipalUIDPrefix{LiteralBefore: "users:", LiteralAfter: ":"}}}}}
	decoded, err := decodeSecurityRole(role)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := security.CompileRoles([]security.Role{decoded}, security.DefaultPolicyLimits()); err != nil {
		t.Fatal(err)
	}
	encoded := encodeSecurityRole(decoded)
	if !proto.Equal(role, encoded) || encoded.Rules[0].GetPrincipalUidPrefix() == nil {
		t.Fatal("UID selector changed in round trip")
	}
	for _, resource := range []*pb.SecurityRule_PrincipalUidPrefix{nil, {}, {PrincipalUidPrefix: &pb.SecurityPrincipalUIDPrefix{}}} {
		role.Rules[0].Resource = resource
		decoded, err := decodeSecurityRole(role)
		if err == nil {
			_, err = security.CompileRoles([]security.Role{decoded}, security.DefaultPolicyLimits())
		}
		if err == nil {
			t.Fatal("nil/empty UID selector broadened")
		}
	}
	role.Rules[0].Resource = &pb.SecurityRule_PrincipalUidPrefix{PrincipalUidPrefix: &pb.SecurityPrincipalUIDPrefix{LiteralBefore: "users:", LiteralAfter: ":"}}
	role.Rules[0].ResourceKind = pb.SecurityResource_SECURITY_RESOURCE_SECURITY
	decoded, err = decodeSecurityRole(role)
	if err == nil {
		_, err = security.CompileRoles([]security.Role{decoded}, security.DefaultPolicyLimits())
	}
	if err == nil {
		t.Fatal("UID selector became global authority")
	}
	invalid := security.Role{ID: "invalid", Rules: []security.PermissionRule{{Permission: security.Read, Resource: security.DataResource}}}
	if encodeSecurityRole(invalid) != nil {
		t.Fatal("missing data selector became Global")
	}
	read := encodeSecurityUser(security.Principal{UserUID: "11111111111111111111111111111111"})
	if read.GetUserId() == "" {
		t.Fatal("read-only UID omitted")
	}
}
