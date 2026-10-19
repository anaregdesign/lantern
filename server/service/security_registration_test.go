package service

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

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
