package provider

import "testing"

func TestSecurityWorkloadBindingRequiresExactOwnedAuthority(t *testing.T) {
	if err := (*SecurityRuntime)(nil).bindWorkload(nil); err == nil {
		t.Fatal("missing security owner")
	}
	if err := (&SecurityRuntime{mode: "off"}).bindWorkload(nil); err != nil {
		t.Fatal("standalone OFF needs no workload", err)
	}
	if err := (&SecurityRuntime{mode: "oidc"}).bindWorkload(nil); err == nil {
		t.Fatal("OIDC missing its quorum authority")
	}
	if err := (&SecurityRuntime{mode: "off"}).bindWorkload(&PeerIdentityRuntime{}); err == nil {
		t.Fatal("unverified workload was bound")
	}
}
