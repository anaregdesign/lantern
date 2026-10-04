package provider

import "testing"

func TestConfiguredPeerCannotInferReplicaAuthority(t *testing.T) {
	if result, err := NewConfiguredSecurityPeer(nil, nil); err == nil || result != nil {
		t.Fatal("missing security boundary accepted")
	}
	off := &SecurityRuntime{mode: "off"}
	if result, err := NewConfiguredSecurityPeer(off, nil); err != nil || result != nil {
		t.Fatal(err)
	}
	if result, err := NewConfiguredSecurityPeer(&SecurityRuntime{mode: "oidc", config: SecurityConfig{NodeRole: "replica"}}, nil); err == nil || result != nil {
		t.Fatal("unleased replica accepted")
	}
	if transport, err := NewWorkloadPeerTransport(nil); err != nil || transport != nil {
		t.Fatal("standalone transport gained credentials")
	}
	if resolver := NewWorkloadPeerResolver(nil); resolver.Source != nil {
		t.Fatal("standalone resolver inferred unapproved peers")
	}
}
