package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestInitializeSecurityPeerRequiresGeneratedOwnedAuthority(t *testing.T) {
	if err := initializeSecurityPeer(t.Context(), SecurityConfig{Mode: "oidc"}, nil); err != nil {
		t.Fatal("external provisioning acquired generated peer initialization", err)
	}
	state := filepath.Join(t.TempDir(), "membership")
	peer := &PeerPlaneConfig{ListenAddress: "127.0.0.1:17380", Identity: PeerIdentityConfig{StateFile: state}}
	for _, pair := range []struct {
		mode    string
		current *security.CurrentAuthority
	}{{"off", nil}, {"oidc", nil}, {"oidc", &security.CurrentAuthority{}}} {
		if err := initializeSecurityPeer(t.Context(), SecurityConfig{Mode: pair.mode, ProvisionedPeer: peer}, pair.current); err == nil {
			t.Fatal("unowned control authority initialized peer custody")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := initializeSecurityPeer(ctx, SecurityConfig{Mode: "oidc", ProvisionedPeer: peer}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("initialization ignored cancellation", err)
	}
	if _, err := os.Lstat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid initialization created persistent peer state", err)
	}
	if peer.Identity.StateMode != "" || peer.Identity.AuthMode != "" {
		t.Fatal("initialization modified immutable source configuration")
	}
}
