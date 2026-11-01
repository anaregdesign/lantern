package config

import (
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestNetworkLoading(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cleanEnvironment(t)
		cfg := Load(":9090")
		want := NetConfig{Port: 6380, MaxRecvMsgBytes: 16 << 20, MaxSendMsgBytes: 16 << 20, MaxConcurrentStreams: 1024}
		if cfg.Net != want || cfg.RateLimit.RPS != 0 || cfg.RateLimit.Burst != 0 {
			t.Fatalf("network defaults = %+v, %+v", cfg.Net, cfg.RateLimit)
		}
	})
	t.Run("overrides and rate-derived burst", func(t *testing.T) {
		cleanEnvironment(t)
		t.Setenv("LANTERN_PORT", "6381")
		t.Setenv("LANTERN_MAX_RECV_MSG_BYTES", "2048")
		t.Setenv("LANTERN_MAX_SEND_MSG_BYTES", "4096")
		t.Setenv("LANTERN_MAX_CONCURRENT_STREAMS", "0")
		t.Setenv("LANTERN_RATE_LIMIT_RPS", "2.5")
		t.Setenv("LANTERN_RATE_LIMIT_BURST", "-1")
		cfg := Load(":9090")
		want := NetConfig{Port: 6381, MaxRecvMsgBytes: 2048, MaxSendMsgBytes: 4096, MaxConcurrentStreams: 0}
		if cfg.Net != want || cfg.RateLimit.RPS != 2.5 || cfg.RateLimit.Burst != 5 {
			t.Fatalf("network overrides = %+v, %+v", cfg.Net, cfg.RateLimit)
		}
	})
	t.Run("malformed values retain parser findings", func(t *testing.T) {
		cleanEnvironment(t)
		t.Setenv("LANTERN_PORT", "bad")
		t.Setenv("LANTERN_MAX_CONCURRENT_STREAMS", "4294967296")
		cfg := Load(":9090")
		if cfg.Net.Port != 6380 || cfg.Net.MaxConcurrentStreams != 1024 || len(envconfig.Findings()) != 2 {
			t.Fatalf("malformed network = %+v; findings = %+v", cfg.Net, envconfig.Findings())
		}
	})
}
