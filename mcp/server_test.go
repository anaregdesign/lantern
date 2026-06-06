package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig_DefaultsWhenUnset(t *testing.T) {
	t.Setenv("LANTERN_ADDR", "")
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "")
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig() returned error: %v", err)
	}
	if cfg.LanternAddr != "http://localhost:6380" {
		t.Fatalf("LanternAddr = %q, want http://localhost:6380", cfg.LanternAddr)
	}
	if cfg.PingTimeout != 5*time.Second {
		t.Fatalf("PingTimeout = %v, want 5s", cfg.PingTimeout)
	}
	if cfg.Logger == nil {
		t.Fatal("Logger = nil, want non-nil")
	}
}

func TestDefaultConfig_HonorsEnv(t *testing.T) {
	t.Setenv("LANTERN_ADDR", "lantern.internal:9000")
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "750ms")
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig() returned error: %v", err)
	}
	if cfg.LanternAddr != "lantern.internal:9000" {
		t.Fatalf("LanternAddr = %q, want lantern.internal:9000", cfg.LanternAddr)
	}
	if cfg.PingTimeout != 750*time.Millisecond {
		t.Fatalf("PingTimeout = %v, want 750ms", cfg.PingTimeout)
	}
}

func TestDefaultConfig_RejectsMalformedTimeout(t *testing.T) {
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "fortnight")
	_, err := DefaultConfig()
	if err == nil {
		t.Fatal("DefaultConfig() returned nil error for malformed timeout")
	}
	if !strings.Contains(err.Error(), "LANTERN_MCP_PING_TIMEOUT") {
		t.Fatalf("error %q does not mention the env var name", err)
	}
}

func TestDefaultConfig_RejectsNonPositiveTimeout(t *testing.T) {
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "0s")
	_, err := DefaultConfig()
	if err == nil {
		t.Fatal("DefaultConfig() returned nil error for 0s timeout")
	}
}
