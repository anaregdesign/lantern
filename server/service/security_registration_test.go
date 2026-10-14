package service

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

func TestRegistrationPolicyReadRequiresManagement(t *testing.T) {
	h, _, ctx := securityAPIFixture(t)
	if _, err := h.GetRegistrationPolicy(ctx("reader", time.Time{}), connect.NewRequest(&pb.GetRegistrationPolicyRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("reader disclosed registration policy", err)
	}
	response, err := h.GetRegistrationPolicy(ctx("admin", time.Time{}), connect.NewRequest(&pb.GetRegistrationPolicyRequest{}))
	if err != nil || response.Msg.Policy != nil || response.Msg.Version == nil || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("absent policy read", err)
	}
}

func TestRegistrationRPCRequiresCurrentComposition(t *testing.T) {
	r := &AuthenticationAdapter{}
	_, begin := r.BeginNativeRegistration(t.Context(), connect.NewRequest(&pb.BeginNativeRegistrationRequest{}))
	_, prepare := r.PrepareNativeRegistration(t.Context(), connect.NewRequest(&pb.PrepareNativeRegistrationRequest{}))
	_, apply := r.ApplyNativeRegistration(t.Context(), connect.NewRequest(&pb.ApplyNativeRegistrationRequest{}))
	_, status := r.GetNativeRegistrationStatus(t.Context(), connect.NewRequest(&pb.GetNativeRegistrationStatusRequest{}))
	for _, err := range []error{begin, prepare, apply, status} {
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("uncomposed registration RPC accepted", err)
		}
	}
	for _, path := range []string{graphv1connect.LanternNativeAuthServiceBeginNativeRegistrationProcedure, graphv1connect.LanternNativeAuthServicePrepareNativeRegistrationProcedure, graphv1connect.LanternNativeAuthServiceApplyNativeRegistrationProcedure, graphv1connect.LanternNativeAuthServiceGetNativeRegistrationStatusProcedure} {
		if !nativeBootstrapProcedure(path) {
			t.Fatal("registration incorrectly requires ordinary Admission")
		}
	}
	if nativeBootstrapProcedure(graphv1connect.LanternNativeAuthServiceGetNativeSessionProcedure) {
		t.Fatal("session read escaped ordinary Admission")
	}
	for _, values := range [][]string{{""}, {"a", "b"}} {
		if _, err := nativeAffinity(http.Header{NativeAffinityHeader: values}); err == nil {
			t.Fatal("ambiguous affinity accepted")
		}
	}
}
