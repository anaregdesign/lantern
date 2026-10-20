package provider

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/anaregdesign/lantern/server/service"
)

func TestPublicCertificationRequiresExactInstalledServicesAndTransport(t *testing.T) {
	data := securityDataFixture(t)
	cfg := SecurityConfig{Mode: "off"}
	runtime, closeRuntime, err := NewSecurityRuntime(t.Context(), cfg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	primary := data.NewLanternService(nil)
	peer, err := data.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, primary)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewRuntimeCertified(data, primary, peer, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, runtimeCertified{}); err == nil {
		t.Fatal("missing data certification accepted")
	}
	foreign := service.NewLanternService(data.GraphCache())
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, foreign, cert); err == nil {
		t.Fatal("foreign service certified")
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, &PeerIdentityRuntime{}, data, primary, cert); err == nil {
		t.Fatal("unbound workload certified")
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert); err != nil {
		t.Fatal(err)
	}
	runtime.mode = "oidc"
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, cert); err == nil {
		t.Fatal("OIDC without its authority was certified")
	}

}
func TestPublicIngressRejectsSpoofedOrAmbiguousGateway(t *testing.T) {
	runtime := &SecurityRuntime{mode: "oidc", config: SecurityConfig{TrustedProxyIPs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}}
	for _, test := range []struct {
		remote      string
		tls         bool
		proto, host []string
		want        bool
	}{
		{"127.0.0.1:100", false, []string{"https"}, []string{"api.example"}, true},
		{"127.0.0.2:100", false, []string{"https"}, []string{"api.example"}, false},
		{"127.0.0.1:100", false, []string{"https", "http"}, []string{"api.example"}, false},
		{"127.0.0.1:100", false, []string{"https"}, []string{"other.example"}, false},
		{"127.0.0.2:100", true, nil, nil, true},
	} {
		req := httptest.NewRequest(http.MethodPost, "https://api.example/rpc", nil)
		req.TLS = nil
		req.RemoteAddr = test.remote
		if test.tls {
			req.TLS = &tls.ConnectionState{}
		}
		req.Header["X-Forwarded-Proto"] = test.proto
		req.Header["X-Forwarded-Host"] = test.host
		if got := runtime.protectedIngress(req); got != test.want {
			t.Fatalf("ingress %v=%v", test, got)
		}
	}
}
