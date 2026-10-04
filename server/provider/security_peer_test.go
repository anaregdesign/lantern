package provider

import "testing"

func TestSecurityPeerRefusesUncertifiedComposition(t *testing.T) {
	for _, runtime := range []*SecurityRuntime{nil, {mode: "off"}, {mode: "oidc"}} {
		if result, err := NewSecurityPeerRuntime(runtime, nil); err == nil || result != nil {
			t.Fatal("uncertified composition admitted", err)
		}
	}
	var runtime *SecurityPeerRuntime
	if runtime.Ready(t.Context()) || runtime.Renew(t.Context()) == nil {
		t.Fatal("unconfigured runtime has authority")
	}
}
