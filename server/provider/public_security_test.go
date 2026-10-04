package provider

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/anaregdesign/lantern/server/service"
)

func TestPublicCertificationRequiresExactInstalledServicesAndTransport(t *testing.T) {
	cfg, data, clock := securityRuntimeFixture(t)
	runtime, closeRuntime, err := NewSecurityRuntime(cfg, data)
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
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, nil, data, primary, cert); err == nil {
		t.Fatal("plaintext protected runtime certified")
	}
	runtime.config.TrustedProxyIPs = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, nil, data, primary, runtimeCertified{}); err == nil {
		t.Fatal("missing data certification accepted")
	}
	foreign := service.NewLanternService(data.GraphCache())
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, nil, data, foreign, cert); err == nil {
		t.Fatal("foreign service certified")
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, &SecurityPeerRuntime{runtime: &SecurityRuntime{}}, data, primary, cert); err == nil {
		t.Fatal("foreign policy runtime certified")
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, &PeerIdentityRuntime{}, nil, data, primary, cert); err == nil {
		t.Fatal("unbound workload certified")
	}
	if _, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, nil, data, primary, cert); err != nil {
		t.Fatal(err)
	}
	if runtime.Ready(context.Background()) {
		t.Fatal("restart fence bypassed")
	}
	clock.advance(35 * 1e9)
	if !runtime.Ready(context.Background()) {
		t.Fatal("qualified writer unavailable after barrier")
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
