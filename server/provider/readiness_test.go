package provider

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/replication"
)

func TestNewReadinessGate(t *testing.T) {
	t.Run("single instance starts ready", func(t *testing.T) {
		gate := NewReadinessGate(ReadinessConfig{MaxLag: 100}, PeerConfig{}, &PeerResolver{}, NewHealthChecker())
		if !gate.Ready() {
			t.Fatal("single-instance readiness gate must start ready")
		}
	})

	t.Run("static peers require bootstrap", func(t *testing.T) {
		gate := NewReadinessGate(ReadinessConfig{MaxLag: 100}, PeerConfig{Peers: []string{"peer:6380"}}, &PeerResolver{}, NewHealthChecker())
		if gate.Ready() {
			t.Fatal("static-peer readiness gate must wait for bootstrap")
		}
	})

	t.Run("dynamic peers require bootstrap", func(t *testing.T) {
		resolver := &PeerResolver{Source: replication.StaticSource{Peers: []string{"peer:6380"}}}
		gate := NewReadinessGate(ReadinessConfig{MaxLag: 100}, PeerConfig{}, resolver, NewHealthChecker())
		if gate.Ready() {
			t.Fatal("dynamic-peer readiness gate must wait for bootstrap")
		}
		gate.OnPumpConnect("peer:6380")
		if !gate.Ready() {
			t.Fatal("dynamic-peer readiness gate must become ready after bootstrap")
		}
	})
}

func TestWorkloadReadinessRefusesUnprovenSingleton(t *testing.T) {
	for _, name := range []string{"empty_dynamic", "missing_store", "unfixed_membership", "expired_membership", "static_peers"} {
		t.Run(name, func(t *testing.T) {
			config, _, _ := generatedPeerIdentityFixture(t, 1)
			now := config.Now()
			config.Now = func() time.Time { return now }
			config.TimeBounds = func() (time.Time, time.Time, error) { return now, now, nil }
			peer, cleanup, err := NewPeerIdentityRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			resolver := NewWorkloadPeerResolver(peer)
			pc := PeerConfig{}
			switch name {
			case "empty_dynamic":
				resolver.Source = replication.StaticSource{}
			case "missing_store":
				resolver = NewWorkloadPeerResolver(&PeerIdentityRuntime{config: config})
			case "unfixed_membership":
				peer.config.FixedMembers = nil
			case "expired_membership":
				now = now.Add(2 * time.Hour)
			case "static_peers":
				pc.Peers = []string{"peer:6380"}
			}
			gate := NewReadinessGate(ReadinessConfig{MaxLag: 100}, pc, resolver, NewHealthChecker())
			if gate.Ready() {
				t.Fatal("unproven singleton bypassed bootstrap")
			}
		})
	}
}

func TestWorkloadReadinessFixedTopology(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("members_%d", count), func(t *testing.T) {
			config, _, _ := generatedPeerIdentityFixture(t, count)
			peer, cleanup, err := NewPeerIdentityRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			resolver := NewWorkloadPeerResolver(peer)
			origins, err := resolver.Source.Resolve(context.Background())
			if err != nil || len(origins) != count-1 {
				t.Fatalf("origins = %v, error = %v", origins, err)
			}
			gate := NewReadinessGate(ReadinessConfig{MaxLag: 100}, PeerConfig{}, resolver, NewHealthChecker())
			if got, want := gate.Ready(), count == 1; got != want {
				t.Fatalf("fixed %d-member workload readiness = %t, want %t", count, got, want)
			}
			mux := newMetricsMux(prometheus.NewRegistry(), gate, false)
			probe := func(want int) {
				t.Helper()
				for _, path := range []string{"/readyz", "/healthz/ready"} {
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
					if response.Code != want {
						t.Fatalf("%s = %d, want %d", path, response.Code, want)
					}
				}
			}
			if count != 1 {
				probe(http.StatusServiceUnavailable)
				return
			}
			probe(http.StatusOK)
			gate.SetServingPermission(false)
			probe(http.StatusServiceUnavailable)
			gate.SetServingPermission(true)
			probe(http.StatusOK)
			gate.BeginDrain()
			gate.SetServingPermission(true)
			gate.MarkBootstrapped()
			probe(http.StatusServiceUnavailable)
		})
	}
}
