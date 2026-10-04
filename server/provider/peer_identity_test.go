package provider

import "testing"

func TestPeerIdentityConfigurationFailsClosed(t *testing.T) {
	for _, config := range []PeerIdentityConfig{
		{},
		{SelfIdentity: "spiffe://lantern.test/a", StateMode: "fresh", CAFile: "relative"},
		{SelfIdentity: "spiffe://lantern.test/a", StateMode: "automatic"},
	} {
		if runtime, cleanup, err := NewPeerIdentityRuntime(config); err == nil || runtime != nil || cleanup != nil {
			t.Fatalf("invalid peer configuration accepted: %v", err)
		}
	}
}
