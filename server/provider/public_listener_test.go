package provider

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPublicListenerRequiresExactServiceAndExcludesPrivatePlanes(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	primary := data.NewLanternService(nil)
	rep, err := data.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, primary)
	if err != nil {
		t.Fatal(err)
	}
	limits := NetConfig{MaxRecvMsgBytes: 4 << 20, MaxSendMsgBytes: 4 << 20}
	cert, err := NewRuntimeCertified(data, primary, rep, restored, limits)
	if err != nil {
		t.Fatal(err)
	}
	public, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, nil, data, primary, cert)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listener, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{EnableReflection: true}, CORSConfig{}, primary, runtime, ChangeConfig{}, nil, nil, nil, nil, NewSlowRPCInterceptor(0, logger), NewHealthChecker(), logger, public)
	if err != nil {
		t.Fatal(err)
	}
	foreign := data.NewLanternService(nil)
	if _, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{}, CORSConfig{}, foreign, runtime, ChangeConfig{}, nil, nil, nil, nil, NewSlowRPCInterceptor(0, logger), NewHealthChecker(), logger, public); err == nil {
		t.Fatal("different service accepted certification")
	}
	server.Config.Handler = listener.Server().Handler
	server.Start()
	control := graphv1connect.NewLanternSecurityServiceClient(server.Client(), server.URL)
	capabilities, err := control.GetAuthCapabilities(context.Background(), connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
	if err != nil || capabilities.Msg.GetMode() != pb.AuthMode_AUTH_MODE_OFF || !capabilities.Msg.GetReady() {
		t.Fatal("certified OFF capabilities", capabilities, err)
	}
	for _, path := range []string{graphv1connect.LanternReplicationServiceSubscribeProcedure, graphv1connect.LanternSecurityPeerServiceRenewPolicyLeaseProcedure} {
		request, err := http.NewRequest(http.MethodPost, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatal("private API registered on public mux", path, response.StatusCode)
		}
	}
}

func TestPublicListenerOIDCRestartFenceClockBoundary(t *testing.T) {
	config, data, clock := securityRuntimeFixture(t)
	token, err := security.NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, valid := security.MachineTokenDigest(token)
	if !valid {
		t.Fatal("generated machine token is invalid")
	}
	prefix := "tenant:"
	config.Bootstrap.Roles = []security.Role{{ID: "worker", Rules: []security.PermissionRule{
		{ID: "read", Effect: security.Allow, Action: security.VertexRead, Resource: security.DataResource, Prefix: &prefix},
		{ID: "write", Effect: security.Allow, Action: security.VertexWrite, Resource: security.DataResource, Prefix: &prefix},
	}}}
	identity := security.Identity{Kind: security.MachinePrincipal, MachineName: "worker"}
	config.Bootstrap.Machines = []security.BootstrapMachine{{Name: "worker", RoleIDs: []string{"worker"}, Credentials: []security.MachineCredential{{Identity: identity, Digest: digest, CreatedAt: clock.Now(), ExpiresAt: clock.Now().Add(time.Hour)}}}}
	runtime, cleanup, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	primary := data.NewLanternService(nil)
	replication, err := data.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, primary)
	if err != nil {
		t.Fatal(err)
	}
	limits := NetConfig{MaxRecvMsgBytes: 4 << 20, MaxSendMsgBytes: 4 << 20}
	certified, err := NewRuntimeCertified(data, primary, replication, restored, limits)
	if err != nil {
		t.Fatal(err)
	}
	tlsSource := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsSource.Close()
	key, err := x509.MarshalPKCS8PrivateKey(tlsSource.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tlsConfig := TLSConfig{CertFile: filepath.Join(dir, "server.pem"), KeyFile: filepath.Join(dir, "server.key")}
	for path, block := range map[string]*pem.Block{
		tlsConfig.CertFile: {Type: "CERTIFICATE", Bytes: tlsSource.Certificate().Raw},
		tlsConfig.KeyFile:  {Type: "PRIVATE KEY", Bytes: key},
	} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	public, err := NewPublicSecurityCertified(runtime, tlsConfig, nil, nil, data, primary, certified)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listener, err := NewPublicLanternListener(server.Listener, limits, tlsConfig, ObservabilityConfig{}, CORSConfig{}, primary, runtime, ChangeConfig{}, nil, nil, nil, nil, NewSlowRPCInterceptor(0, logger), NewHealthChecker(), logger, public)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = listener.Server().Handler
	server.TLS = listener.Server().TLSConfig.Clone()
	server.StartTLS()
	control := graphv1connect.NewLanternSecurityServiceClient(server.Client(), server.URL)
	client := graphv1connect.NewLanternServiceClient(server.Client(), server.URL)
	ctx := t.Context()
	request := func(key string) *connect.Request[pb.GetVertexRequest] {
		req := connect.NewRequest(&pb.GetVertexRequest{Key: key})
		req.Header().Set("Authorization", "Bearer "+token)
		return req
	}
	assertCapabilities := func(t *testing.T, ready bool) {
		t.Helper()
		caps, err := control.GetAuthCapabilities(ctx, connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
		if err != nil || caps.Msg.GetMode() != pb.AuthMode_AUTH_MODE_OIDC || caps.Msg.GetReady() != ready {
			t.Fatalf("OIDC readiness want %v: %v, %v", ready, caps, err)
		}
	}
	assertMissing := func(t *testing.T, code connect.Code) {
		t.Helper()
		if _, err := client.GetVertex(ctx, request("tenant:one")); connect.CodeOf(err) != code {
			t.Fatalf("authenticated missing-key read want %v: %v", code, err)
		}
	}
	t.Run("startup clock fixed across both RPCs", func(t *testing.T) {
		assertCapabilities(t, false)
		assertMissing(t, connect.CodeUnavailable)
	})
	clock.advance(35*time.Second - time.Nanosecond)
	t.Run("last fenced nanosecond fixed across both RPCs", func(t *testing.T) {
		assertCapabilities(t, false)
		assertMissing(t, connect.CodeUnavailable)
	})
	t.Run("separate RPCs cross legitimate expiry", func(t *testing.T) {
		assertCapabilities(t, false)
		clock.advance(time.Nanosecond)
		assertMissing(t, connect.CodeNotFound)
	})
	t.Run("exact expiry permits role scoped data", func(t *testing.T) {
		assertCapabilities(t, true)
		assertMissing(t, connect.CodeNotFound)
		put := connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "tenant:one", Value: &pb.Vertex_String_{String_: "live"}, Expiration: timestamppb.New(clock.Now().Add(time.Minute))}})
		put.Header().Set("Authorization", "Bearer "+token)
		if _, err := client.PutVertex(ctx, put); err != nil {
			t.Fatal("role scoped Put at expiry", err)
		}
		got, err := client.GetVertex(ctx, request("tenant:one"))
		if err != nil || got.Msg.GetVertex().GetKey() != "tenant:one" || got.Msg.GetVertex().GetString_() != "live" {
			t.Fatal("role scoped Get at expiry", got, err)
		}
		if _, err := client.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "tenant:one"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("anonymous read accepted", err)
		}
		if _, err := client.GetVertex(ctx, request("private:one")); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("out of prefix read accepted", err)
		}
	})
}
