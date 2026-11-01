package config

import (
	"testing"
	"time"
)

func TestShutdownLoading(t *testing.T) {
	cleanEnvironment(t)
	if got := Load(":9090").Shutdown; got != (ShutdownConfig{Timeout: 30 * time.Second}) {
		t.Fatalf("shutdown defaults = %+v", got)
	}
	t.Setenv("LANTERN_SHUTDOWN_TIMEOUT_SECONDS", "9")
	t.Setenv("LANTERN_DRAIN_DELAY_SECONDS", "3")
	if got := Load(":9090").Shutdown; got != (ShutdownConfig{Timeout: 9 * time.Second, DrainDelay: 3 * time.Second}) {
		t.Fatalf("shutdown overrides = %+v", got)
	}
}
