package graphv1connect_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	graphv1connect "github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

// TestGeneratedSurface is a thin compile-check that the protoc-gen-connect-go
// output exposes the symbols downstream issues (#337 server, #338 sdks/go,
// #339 admin, #340 sdks/node) will rely on. It does not stand up a server;
// it just asserts the generated handler / client constructors and the
// service constant exist with the expected signatures.
func TestGeneratedSurface(t *testing.T) {
	t.Parallel()

	if graphv1connect.LanternServiceName != "graph.v1.LanternService" {
		t.Fatalf("LanternServiceName = %q, want %q",
			graphv1connect.LanternServiceName, "graph.v1.LanternService")
	}
	if graphv1connect.LanternReplicationServiceName != "graph.v1.LanternReplicationService" {
		t.Fatalf("LanternReplicationServiceName = %q, want %q",
			graphv1connect.LanternReplicationServiceName, "graph.v1.LanternReplicationService")
	}

	// Handler-side: the Unimplemented* handlers must satisfy the handler
	// interface so #337 can wrap them with a Connect mux during the
	// transition window.
	var lanternHandler graphv1connect.LanternServiceHandler = graphv1connect.UnimplementedLanternServiceHandler{}
	var replicationHandler graphv1connect.LanternReplicationServiceHandler = graphv1connect.UnimplementedLanternReplicationServiceHandler{}
	if _, h := graphv1connect.NewLanternServiceHandler(lanternHandler); h == nil {
		t.Fatal("NewLanternServiceHandler returned nil http.Handler")
	}
	if _, h := graphv1connect.NewLanternReplicationServiceHandler(replicationHandler); h == nil {
		t.Fatal("NewLanternReplicationServiceHandler returned nil http.Handler")
	}

	// Client-side: the constructors must accept a stdlib http.Client and a
	// base URL, matching the surface #338 (Go SDK) builds against.
	httpClient := &http.Client{}
	if c := graphv1connect.NewLanternServiceClient(httpClient, "http://example.invalid"); c == nil {
		t.Fatal("NewLanternServiceClient returned nil")
	}
	if c := graphv1connect.NewLanternReplicationServiceClient(httpClient, "http://example.invalid"); c == nil {
		t.Fatal("NewLanternReplicationServiceClient returned nil")
	}

	// connectrpc.com/connect must be available at the version pinned in
	// pb/go.mod. The IsAtLeastVersion1_13_0 constant from the generated
	// file already enforces a lower bound; this just keeps an explicit
	// reference so the dependency is exercised by `go test`.
	_ = connect.CodeUnknown
}

// This is generated transport conformance, not Server authorization: the leaf
// module owns wire fidelity/routing while production admission is tested by
// the real Server integration suite.
func TestSecurityAndChangeWire(t *testing.T) {
	for _, profile := range []struct {
		name string
		opts []connect.ClientOption
	}{
		{"Connect protobuf", nil},
		{"Connect JSON", []connect.ClientOption{connect.WithProtoJSON()}},
		{"gRPC", []connect.ClientOption{connect.WithGRPC()}},
		{"gRPC-Web", []connect.ClientOption{connect.WithGRPCWeb()}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			issuer := "https://idp.example"
			identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: issuer, Subject: "literal:subject"}
			version := &pb.SecurityVersion{Revision: 8, Digest: bytes.Repeat([]byte{4}, 32), Generation: bytes.Repeat([]byte{5}, 16)}
			change := &pb.ApplySecurityChangesRequest{ExpectedRevision: 7, ChangeId: bytes.Repeat([]byte{6}, 16), Changes: []*pb.SecurityChange{
				{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "reader", Name: "Reader", Rules: []*pb.SecurityRule{
					{Id: "all", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: ""}},
					{Id: "deny", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "tenant:private:"}},
				}}}},
				{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "reader"}}},
			}}
			mux := http.NewServeMux()
			path, handler := graphv1connect.NewLanternSecurityServiceHandler(controlWireHandler{t: t, identity: identity, version: version, change: change})
			if path != "/graph.v1.LanternSecurityService/" {
				t.Fatalf("control mount = %q", path)
			}
			mux.Handle(path, handler)
			frames := []*pb.WatchChangesResponse{
				{Bootstrap: true, Cursor: []byte{1, 2}},
				{Invalidations: []*pb.ChangeInvalidation{{Identity: &pb.ChangeInvalidation_VertexKey{VertexKey: "tenant:literal:%2F"}}}},
				{Invalidations: []*pb.ChangeInvalidation{{Identity: &pb.ChangeInvalidation_EdgeKey{EdgeKey: &pb.EdgeKey{Tail: "tenant:tail", Head: "tenant:head"}}, CurrentImage: &pb.ChangeInvalidation_Edge{Edge: &pb.Edge{Tail: "tenant:tail", Head: "tenant:head", Weight: 2.5}}}}, Cursor: []byte{3, 4}},
			}
			watch := &pb.WatchChangesRequest{Prefix: "tenant:", Projection: pb.ChangeProjection_CHANGE_PROJECTION_VALUE, Bootstrap: true}
			path, handler = graphv1connect.NewLanternChangeServiceHandler(changeWireHandler{t: t, request: watch, frames: frames})
			if path != "/graph.v1.LanternChangeService/" {
				t.Fatalf("CDC mount = %q", path)
			}
			mux.Handle(path, handler)
			server := httptest.NewUnstartedServer(mux)
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			client := graphv1connect.NewLanternSecurityServiceClient(server.Client(), server.URL+"/", profile.opts...)
			capabilities, err := client.GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
			if err != nil || capabilities.Msg.GetMode() != pb.AuthMode_AUTH_MODE_OIDC || !capabilities.Msg.GetReady() || capabilities.Msg.GetProtocolVersion() != 1 || capabilities.Msg.GetLoginPath() != "/auth/login" || len(capabilities.Msg.GetLoginIssuers()) != 1 || capabilities.Msg.GetLoginIssuers()[0].GetIssuer() != issuer {
				t.Fatalf("capabilities wire mismatch: response=%v error=%v", capabilities, err)
			}
			principal, err := client.GetCurrentPrincipal(ctx, connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
			if err != nil || !proto.Equal(principal.Msg.GetIdentity(), identity) || principal.Msg.GetRecentAuthentication() || principal.Msg.GetExpiresAt() != nil || principal.Msg.GetCsrfToken() != "" || !proto.Equal(principal.Msg.GetVersion(), version) {
				t.Fatalf("unknown recent-auth wire mismatch: response=%v error=%v", principal, err)
			}
			applied, err := client.ApplySecurityChanges(ctx, connect.NewRequest(change))
			if err != nil || !proto.Equal(applied.Msg.GetVersion(), version) || !applied.Msg.GetReplayed() || applied.Msg.GetEnforcement() != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING || len(applied.Msg.GetApplied()) != 2 || !applied.Msg.GetApplied()[0] || applied.Msg.GetApplied()[1] {
				t.Fatalf("aligned outcomes wire mismatch: response=%v error=%v", applied, err)
			}
			issuerResponse, err := client.GetIssuer(ctx, connect.NewRequest(&pb.GetIssuerRequest{Issuer: issuer}))
			if err != nil || !proto.Equal(issuerResponse.Msg.GetVersion(), version) {
				t.Fatalf("Issuer version mismatch: response=%v error=%v", issuerResponse, err)
			}
			gotIssuer := issuerResponse.Msg.GetIssuer()
			if gotIssuer.GetIssuer() != issuer || !gotIssuer.GetEnabled() || gotIssuer.GetClientId() != "web-client" || gotIssuer.GetApiAudience() != "api-audience" || gotIssuer.GetRedirectUri() != "https://admin.example/auth/callback" || len(gotIssuer.GetAlgorithms()) != 1 || gotIssuer.GetAlgorithms()[0] != "RS256" || gotIssuer.SecretRef != nil || gotIssuer.GetConfigRevision() != 3 || !gotIssuer.GetEnvOwned() || gotIssuer.GetDeleted() || !gotIssuer.GetHasSecretBinding() {
				t.Fatal("read-only Issuer projection or optional secret absence changed")
			}
			user, err := client.GetUser(ctx, connect.NewRequest(&pb.GetUserRequest{Identity: identity}))
			if err != nil || !proto.Equal(user.Msg.GetVersion(), version) || !proto.Equal(user.Msg.GetUser().GetIdentity(), identity) || user.Msg.GetUser().GetState() != pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE || len(user.Msg.GetUser().GetAssignments()) != 1 {
				t.Fatalf("user read model mismatch: response=%v error=%v", user, err)
			}
			assignment := user.Msg.GetUser().GetAssignments()[0]
			if !proto.Equal(assignment.GetIdentity(), identity) || assignment.GetRoleId() != "reader" || !assignment.GetEnvOwned() {
				t.Fatal("exact membership/immutable environment lock changed")
			}
			// Empty optional key and omitted key are distinct wire intents. Server
			// policy validation, rather than protobuf defaults, decides their validity.
			explanation, err := client.ExplainAccess(ctx, connect.NewRequest(&pb.ExplainAccessRequest{Identity: identity, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, LogicalKey: proto.String("")}))
			if err != nil || explanation.Msg.GetAllowed() || !proto.Equal(explanation.Msg.GetVersion(), version) || len(explanation.Msg.GetMatches()) != 1 {
				t.Fatalf("explanation read model mismatch: response=%v error=%v", explanation, err)
			}
			match := explanation.Msg.GetMatches()[0]
			if match.GetRoleId() != "reader" || match.GetRuleId() != "deny" || match.GetEffect() != pb.SecurityEffect_SECURITY_EFFECT_DENY || match.GetAction() != pb.SecurityAction_SECURITY_ACTION_VERTEX_READ || match.GetEndpoint() != "" {
				t.Fatal("explanation source/action changed")
			}
			changes := graphv1connect.NewLanternChangeServiceClient(server.Client(), server.URL, profile.opts...)
			stream, err := changes.WatchChanges(ctx, connect.NewRequest(watch))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			for i, frame := range frames {
				if !stream.Receive() || !proto.Equal(stream.Msg(), frame) {
					t.Fatalf("CDC frame %d mismatch: error=%v", i, stream.Err())
				}
			}
			if stream.Receive() || stream.Err() != nil {
				t.Fatalf("CDC terminal state: %v", stream.Err())
			}
			if len(frames[1].GetCursor()) != 0 || frames[1].GetInvalidations()[0].GetCurrentImage() != nil || frames[1].GetInvalidations()[0].GetVertexKey() != "tenant:literal:%2F" || frames[2].GetInvalidations()[0].GetEdgeKey().GetHead() != "tenant:head" || frames[2].GetInvalidations()[0].GetEdge().GetWeight() != 2.5 {
				t.Fatal("partial identity/value distinction lost")
			}
			// The generated public mounts do not implicitly expose the peer service.
			response, err := server.Client().Post(server.URL+graphv1connect.LanternSecurityPeerServiceRenewPolicyLeaseProcedure, "application/json", bytes.NewBufferString("{}"))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("peer route appeared on public test mux: %d", response.StatusCode)
			}
		})
	}
}

type controlWireHandler struct {
	graphv1connect.UnimplementedLanternSecurityServiceHandler
	t        *testing.T
	identity *pb.SecurityIdentity
	version  *pb.SecurityVersion
	change   *pb.ApplySecurityChangesRequest
}

func (h controlWireHandler) GetAuthCapabilities(context.Context, *connect.Request[pb.GetAuthCapabilitiesRequest]) (*connect.Response[pb.GetAuthCapabilitiesResponse], error) {
	return connect.NewResponse(&pb.GetAuthCapabilitiesResponse{Mode: pb.AuthMode_AUTH_MODE_OIDC, Ready: true, ProtocolVersion: 1, LoginPath: "/auth/login", LoginIssuers: []*pb.LoginIssuer{{Issuer: h.identity.Issuer, Label: "Example"}}}), nil
}
func (h controlWireHandler) GetCurrentPrincipal(context.Context, *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
	return connect.NewResponse(&pb.GetCurrentPrincipalResponse{Identity: h.identity, Version: h.version}), nil
}
func (h controlWireHandler) ApplySecurityChanges(_ context.Context, request *connect.Request[pb.ApplySecurityChangesRequest]) (*connect.Response[pb.ApplySecurityChangesResponse], error) {
	if !proto.Equal(request.Msg, h.change) || request.Msg.GetExpectedRevision() != 7 || len(request.Msg.GetChangeId()) != 16 || request.Msg.GetChanges()[0].GetPutRole().GetRules()[0].GetResource() == nil || request.Msg.GetChanges()[1].GetPutAssignment().GetRoleId() != "reader" {
		h.t.Error("management oneof/prefix/identity changed across wire")
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	return connect.NewResponse(&pb.ApplySecurityChangesResponse{Version: h.version, Applied: []bool{true, false}, Replayed: true, Enforcement: pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING}), nil
}
func (h controlWireHandler) GetIssuer(_ context.Context, request *connect.Request[pb.GetIssuerRequest]) (*connect.Response[pb.GetIssuerResponse], error) {
	if request.Msg.GetIssuer() != h.identity.Issuer {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	return connect.NewResponse(&pb.GetIssuerResponse{Version: h.version, Issuer: &pb.SecurityIssuer{Issuer: h.identity.Issuer, Enabled: true, ClientId: "web-client", ApiAudience: "api-audience", RedirectUri: "https://admin.example/auth/callback", Algorithms: []string{"RS256"}, ConfigRevision: 3, EnvOwned: true, HasSecretBinding: true}}), nil
}
func (h controlWireHandler) GetUser(_ context.Context, request *connect.Request[pb.GetUserRequest]) (*connect.Response[pb.GetUserResponse], error) {
	if !proto.Equal(request.Msg.GetIdentity(), h.identity) {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	return connect.NewResponse(&pb.GetUserResponse{Version: h.version, User: &pb.SecurityUser{Identity: h.identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE, Assignments: []*pb.SecurityRoleAssignment{{Identity: h.identity, RoleId: "reader", EnvOwned: true}}}}), nil
}
func (h controlWireHandler) ExplainAccess(_ context.Context, request *connect.Request[pb.ExplainAccessRequest]) (*connect.Response[pb.ExplainAccessResponse], error) {
	if !proto.Equal(request.Msg.GetIdentity(), h.identity) || request.Msg.GetAction() != pb.SecurityAction_SECURITY_ACTION_VERTEX_READ || request.Msg.LogicalKey == nil || request.Msg.GetLogicalKey() != "" || request.Msg.GetEdge() != nil {
		h.t.Error("optional explanation intent changed across wire")
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	return connect.NewResponse(&pb.ExplainAccessResponse{Version: h.version, Matches: []*pb.SecurityRuleMatch{{RoleId: "reader", RuleId: "deny", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ}}}), nil
}

type changeWireHandler struct {
	graphv1connect.UnimplementedLanternChangeServiceHandler
	t       *testing.T
	request *pb.WatchChangesRequest
	frames  []*pb.WatchChangesResponse
}

func (h changeWireHandler) WatchChanges(_ context.Context, request *connect.Request[pb.WatchChangesRequest], stream *connect.ServerStream[pb.WatchChangesResponse]) error {
	if !proto.Equal(request.Msg, h.request) || request.Msg.GetPrefix() != "tenant:" || request.Msg.GetProjection() != pb.ChangeProjection_CHANGE_PROJECTION_VALUE || !request.Msg.GetBootstrap() || len(request.Msg.GetCursor()) != 0 {
		h.t.Error("CDC request changed across wire")
		return connect.NewError(connect.CodeInvalidArgument, nil)
	}
	for _, frame := range h.frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

func unimplementedUnary[Request, Response any](call func(context.Context, *connect.Request[Request]) (*connect.Response[Response], error)) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := call(ctx, connect.NewRequest(new(Request)))
		return err
	}
}

func TestSecurityUnimplementedRouting(t *testing.T) {
	path, handler := graphv1connect.NewLanternSecurityServiceHandler(graphv1connect.UnimplementedLanternSecurityServiceHandler{})
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := graphv1connect.NewLanternSecurityServiceClient(server.Client(), server.URL)
	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"GetAuthCapabilities", unimplementedUnary(client.GetAuthCapabilities)},
		{"GetCurrentPrincipal", unimplementedUnary(client.GetCurrentPrincipal)},
		{"ListIssuers", unimplementedUnary(client.ListIssuers)},
		{"GetIssuer", unimplementedUnary(client.GetIssuer)},
		{"ListRoles", unimplementedUnary(client.ListRoles)},
		{"GetRole", unimplementedUnary(client.GetRole)},
		{"ListUsers", unimplementedUnary(client.ListUsers)},
		{"GetUser", unimplementedUnary(client.GetUser)},
		{"ListRoleAssignments", unimplementedUnary(client.ListRoleAssignments)},
		{"ListSecurityAudit", unimplementedUnary(client.ListSecurityAudit)},
		{"GetRoleTemplates", unimplementedUnary(client.GetRoleTemplates)},
		{"ExplainAccess", unimplementedUnary(client.ExplainAccess)},
		{"ValidateIssuer", unimplementedUnary(client.ValidateIssuer)},
		{"ApplySecurityChanges", unimplementedUnary(client.ApplySecurityChanges)},
		{"ApplySecurityChange", unimplementedUnary(client.ApplySecurityChange)},
		{"GetSecurityChangeStatus", unimplementedUnary(client.GetSecurityChangeStatus)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tc.call(ctx); connect.CodeOf(err) != connect.CodeUnimplemented {
				t.Fatalf("generated RPC lost explicit unimplemented error: %v", err)
			}
		})
	}
	response, err := server.Client().Post(server.URL+path+"UnknownMethod", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown method was routed: %d", response.StatusCode)
	}
}

type leaseWireHandler struct {
	graphv1connect.UnimplementedLanternSecurityPeerServiceHandler
	t        *testing.T
	request  *pb.RenewPolicyLeaseRequest
	response *pb.RenewPolicyLeaseResponse
}

func (h leaseWireHandler) RenewPolicyLease(_ context.Context, request *connect.Request[pb.RenewPolicyLeaseRequest]) (*connect.Response[pb.RenewPolicyLeaseResponse], error) {
	if !proto.Equal(request.Msg, h.request) || len(request.Msg.GetReceiver()) != 16 || len(request.Msg.GetBootNonce()) != 16 || len(request.Msg.GetChallenge()) != 32 || len(request.Msg.GetKnownDigest()) != 32 {
		h.t.Error("opaque challenge binding changed across wire")
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	return connect.NewResponse(h.response), nil
}

func TestPrivateLeaseWire(t *testing.T) {
	request := &pb.RenewPolicyLeaseRequest{Receiver: bytes.Repeat([]byte{1}, 16), BootNonce: bytes.Repeat([]byte{2}, 16), Challenge: bytes.Repeat([]byte{3}, 32), KnownDigest: bytes.Repeat([]byte{4}, 32)}
	want := &pb.RenewPolicyLeaseResponse{SignedLease: []byte{0, 255, 1, 0}, SignedCheckpoint: []byte{255, 0, 2, 3, 0}}
	path, handler := graphv1connect.NewLanternSecurityPeerServiceHandler(leaseWireHandler{t: t, request: request, response: want})
	if path != "/graph.v1.LanternSecurityPeerService/" {
		t.Fatalf("peer mount = %q", path)
	}
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	for _, profile := range []struct {
		name string
		opts []connect.ClientOption
	}{{"protobuf", nil}, {"JSON", []connect.ClientOption{connect.WithProtoJSON()}}} {
		t.Run(profile.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := graphv1connect.NewLanternSecurityPeerServiceClient(server.Client(), server.URL+"/", profile.opts...)
			got, err := client.RenewPolicyLease(ctx, connect.NewRequest(request))
			if err != nil || !bytes.Equal(got.Msg.GetSignedLease(), want.SignedLease) || !bytes.Equal(got.Msg.GetSignedCheckpoint(), want.SignedCheckpoint) {
				t.Fatalf("opaque original bytes changed: response=%v error=%v", got, err)
			}
		})
	}
	// A peer-only generated mux has no public management surface either.
	response, err := server.Client().Post(server.URL+graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure, "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("public control route appeared on peer test mux: %d", response.StatusCode)
	}

	_, missing := graphv1connect.NewLanternSecurityPeerServiceHandler(graphv1connect.UnimplementedLanternSecurityPeerServiceHandler{})
	unimplemented := httptest.NewServer(missing)
	defer unimplemented.Close()
	client := graphv1connect.NewLanternSecurityPeerServiceClient(unimplemented.Client(), unimplemented.URL)
	if _, err := client.RenewPolicyLease(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("missing peer implementation did not fail explicitly: %v", err)
	}
}

func TestCDCUnimplementedRouting(t *testing.T) {
	_, handler := graphv1connect.NewLanternChangeServiceHandler(graphv1connect.UnimplementedLanternChangeServiceHandler{})
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := graphv1connect.NewLanternChangeServiceClient(server.Client(), server.URL)
	stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{}))
	if err == nil {
		defer stream.Close()
		if stream.Receive() {
			t.Fatal("unimplemented CDC returned a frame")
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("missing CDC implementation did not fail explicitly: %v", err)
	}
}
