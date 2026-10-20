package provider

import (
	"context"
	"errors"
	"github.com/anaregdesign/lantern/server/internal/security"
	"strings"
	"testing"
)

func TestCurrentDataPeerFactoryRequiresMatchingAuthority(t *testing.T) {
	cfg := PeerPlaneConfig{ListenAddress: "127.0.0.1:17380", Identity: PeerIdentityConfig{AuthMode: "oidc", CurrentProfile: "current-v2:abababababababababababababababababababababababababababababababab", SecurityGeneration: [16]byte{1}, StateMode: "fresh", Deployment: [16]byte{1}, StateFile: "/tmp/state", ManifestFile: "/tmp/manifest", OperatorKeyFile: "/tmp/operator", CertFile: "/tmp/cert", KeyFile: "/tmp/key", CAFile: "/tmp/ca"}}
	for _, runtime := range []*SecurityRuntime{nil, {mode: "oidc", current: &security.CurrentAuthority{}}} {
		if peer, cleanup, err := NewWorkloadPeerIdentity(t.Context(), cfg, runtime); err == nil || peer != nil || cleanup != nil || runtime != nil && !strings.Contains(err.Error(), "different current cohort") {
			t.Fatal("unowned or foreign current authority reached data workload", err)
		}
	}
}

func TestConfiguredStandaloneOFFHasNoTimeOwnerAndHonorsCancellation(t *testing.T) {
	runtime, closeRuntime, err := NewSecurityRuntime(t.Context(), SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	peer, cleanup, err := NewWorkloadPeerIdentity(t.Context(), PeerPlaneConfig{}, runtime)
	if err != nil || peer != nil || runtime.current != nil {
		t.Fatal("standalone OFF gained time dependency", err)
	}
	if cleanup != nil {
		cleanup()
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err = NewWorkloadPeerIdentity(ctx, PeerPlaneConfig{}, runtime); !errors.Is(err, context.Canceled) {
		t.Fatal("startup cancellation lost", err)
	}
	if _, _, err = NewSecurityRuntime(ctx, SecurityConfig{Mode: "off"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("security startup cancellation lost", err)
	}
}
