package config

import (
	"os"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestShutdownLoading(t *testing.T) {
	cleanEnvironment(t)
	reader := envconfig.NewReader(os.Environ())
	if got := Load(reader, ":9090").Shutdown; got != (ShutdownConfig{Timeout: 30 * time.Second}) {
		t.Fatalf("shutdown defaults = %+v", got)
	}
	t.Setenv("LANTERN_SHUTDOWN_TIMEOUT_SECONDS", "9")
	t.Setenv("LANTERN_DRAIN_DELAY_SECONDS", "3")
	reader = envconfig.NewReader(os.Environ())
	if got := Load(reader, ":9090").Shutdown; got != (ShutdownConfig{Timeout: 9 * time.Second, DrainDelay: 3 * time.Second}) {
		t.Fatalf("shutdown overrides = %+v", got)
	}
}
