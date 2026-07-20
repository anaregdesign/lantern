package provider

import (
	"testing"

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
