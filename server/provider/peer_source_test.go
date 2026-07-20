package provider

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/replication"
)

func TestNewPeerResolver(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("static leaves dynamic source disabled", func(t *testing.T) {
		got := NewPeerResolver(PeerConfig{
			Discovery: "static",
			Peers:     []string{"peer:6380"},
		}, logger)
		if got.Source != nil {
			t.Fatalf("static Source = %T, want nil", got.Source)
		}
	})

	t.Run("dns constructs shared source", func(t *testing.T) {
		got := NewPeerResolver(PeerConfig{
			Discovery:         "dns",
			DNSName:           "lantern-headless.default.svc.cluster.local",
			DefaultPort:       "6380",
			DiscoveryInterval: 10 * time.Second,
		}, logger)
		dns, ok := got.Source.(*replication.DNSSource)
		if !ok {
			t.Fatalf("DNS Source = %T, want *replication.DNSSource", got.Source)
		}
		if dns.Name != "lantern-headless.default.svc.cluster.local" || dns.Port != "6380" {
			t.Fatalf("DNS Source = %+v", dns)
		}
	})
}
