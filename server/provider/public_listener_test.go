package provider

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
)

func TestPublicListenerRequiresExactServiceAndExcludesPrivatePlanes(t *testing.T) {
	data := securityDataFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(t.Context(), SecurityConfig{Mode: "off"}, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	primary := data.NewExecutor(nil)
	rep, err := data.NewReplication(primary)
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
	logging, err := NewManagedLogRuntime(t.Context(), runtime, newServingLogger(ObservabilityConfig{}, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	slowOwner := NewSlowRPCInterceptor(0, logger)
	operational := operationalPublicTestCertification(t, runtime, primary, slowOwner, cert)
	public, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert, logging, operational)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	rateOwner, recovery := operational.rate, operational.recovery
	validator := operational.validator
	listener, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{EnableReflection: true}, CORSConfig{}, primary, runtime, ChangeConfig{}, validator, rateOwner, nil, nil, slowOwner, NewHealthChecker(), logger, public, recovery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{}, CORSConfig{}, primary, runtime, ChangeConfig{}, validator, NewRateLimitInterceptor(0, 0), nil, nil, slowOwner, NewHealthChecker(), logger, public, recovery); err == nil {
		t.Fatal("different rate owner accepted recovery certification")
	}
	foreignRecovery := *recovery
	if _, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{}, CORSConfig{}, primary, runtime, ChangeConfig{}, validator, rateOwner, nil, nil, slowOwner, NewHealthChecker(), logger, public, &foreignRecovery); err == nil {
		t.Fatal("equal-valued foreign recovery accepted certification")
	}
	for name, mutate := range map[string]func(*publicSecurityCertified){
		"Rate":             func(c *publicSecurityCertified) { c.rate = NewRateLimitInterceptor(0, 0) },
		"recovery":         func(c *publicSecurityCertified) { c.recovery = &foreignRecovery },
		"missing Search":   func(c *publicSecurityCertified) { c.search = managedSearchCertified{} },
		"missing Capacity": func(c *publicSecurityCertified) { c.capacity = nil },
		"foreign Capacity": func(c *publicSecurityCertified) { c.capacity = &dataruntime.GraphPolicyOwner{} },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := public
			mutate(&wrong)
			// The mounted pair is still genuinely bound. Certification must
			// attest that same pair rather than accepting binding alone.
			if _, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{}, CORSConfig{}, primary, runtime, ChangeConfig{}, validator, rateOwner, nil, nil, slowOwner, NewHealthChecker(), logger, wrong, recovery); err == nil {
				t.Fatal("foreign certified identity accepted the actual mounted pair")
			}
		})
	}
	foreign := data.NewExecutor(nil)
	if _, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{}, CORSConfig{}, foreign, runtime, ChangeConfig{}, validator, rateOwner, nil, nil, slowOwner, NewHealthChecker(), logger, public, recovery); err == nil {
		t.Fatal("different service accepted certification")
	}
	if _, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{}, CORSConfig{}, primary, runtime, ChangeConfig{}, validator, rateOwner, nil, nil, NewSlowRPCInterceptor(0, logger), NewHealthChecker(), logger, public, recovery); err == nil {
		t.Fatal("equal-valued foreign Slow RPC owner accepted certification")
	}
	for _, wrong := range []*ValidationInterceptor{nil, NewValidationInterceptor(validator.limits)} {
		if _, err := NewPublicLanternListener(server.Listener, limits, TLSConfig{}, ObservabilityConfig{}, CORSConfig{}, primary, runtime, ChangeConfig{}, wrong, rateOwner, nil, nil, slowOwner, NewHealthChecker(), logger, public, recovery); err == nil {
			t.Fatal("absent or equal-valued foreign validator accepted certification")
		}
	}
	server.Config.Handler = listener.Server().Handler
	server.Config.Protocols = listener.Server().Protocols
	server.Start()
	control := graphv1connect.NewLanternSecurityServiceClient(server.Client(), server.URL)
	capabilities, err := control.GetAuthCapabilities(context.Background(), connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
	if err != nil || capabilities.Msg.GetMode() != pb.AuthMode_AUTH_MODE_OFF || !capabilities.Msg.GetReady() {
		t.Fatal("certified OFF capabilities", capabilities, err)
	}
	for _, path := range []string{graphv1connect.LanternReplicationServiceSubscribeProcedure, "/graph.v1.LanternSecurityPeerService/RenewPolicyLease"} {
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
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	reflectionClient := &http.Client{Transport: &http.Transport{Protocols: protocols}}
	defer reflectionClient.CloseIdleConnections()
	for _, serviceName := range []string{grpcreflect.ReflectV1ServiceName, grpcreflect.ReflectV1AlphaServiceName} {
		t.Run(serviceName, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			client := connect.NewClient[reflectionv1.ServerReflectionRequest, reflectionv1.ServerReflectionResponse](reflectionClient, server.URL+"/"+serviceName+"/ServerReflectionInfo", connect.WithGRPC())
			stream := client.CallBidiStream(ctx)
			defer stream.CloseResponse()
			if err := stream.Send(&reflectionv1.ServerReflectionRequest{MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""}}); err != nil {
				t.Fatal("send reflection request", err)
			}
			if err := stream.CloseRequest(); err != nil {
				t.Fatal("close reflection request", err)
			}
			response, err := stream.Receive()
			if err != nil || response.GetErrorResponse() != nil || response.GetListServicesResponse() == nil {
				t.Fatal("certified OFF reflection", response, err)
			}
			want := map[string]bool{
				graphv1connect.LanternServiceName:           true,
				graphv1connect.LanternSecurityServiceName:   true,
				graphv1connect.LanternNativeAuthServiceName: true,
				grpchealth.HealthV1ServiceName:              true,
			}
			services := response.GetListServicesResponse().GetService()
			if len(services) != len(want) {
				t.Fatalf("public reflection advertised unexpected services: %v", services)
			}
			for _, service := range services {
				if !want[service.GetName()] {
					t.Fatalf("private or duplicate service advertised by public reflection: %q", service.GetName())
				}
				delete(want, service.GetName())
			}
		})
	}
}
