package provider

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
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
