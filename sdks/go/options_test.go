package client

import (
	"testing"
	"time"

	"google.golang.org/grpc/keepalive"
)

func TestDefaultKeepaliveAboveServerMinTime(t *testing.T) {
	// Server enforces MinTime: 10s; SDK default must stay safely above it.
	if defaultKeepalive.Time < 10*time.Second {
		t.Errorf("defaultKeepalive.Time = %v, must be >= 10s (server MinTime)", defaultKeepalive.Time)
	}
	if !defaultKeepalive.PermitWithoutStream {
		t.Error("defaultKeepalive.PermitWithoutStream must be true")
	}
}

func TestWithKeepaliveParamsOverrides(t *testing.T) {
	custom := keepalive.ClientParameters{Time: time.Minute, Timeout: 5 * time.Second, PermitWithoutStream: false}
	var o options
	WithKeepaliveParams(custom)(&o)
	if o.keepalive != custom {
		t.Errorf("keepalive = %+v, want %+v", o.keepalive, custom)
	}
}

func TestWithOpenTelemetryAppendsDialOption(t *testing.T) {
	var o options
	before := len(o.dialOptions)
	WithOpenTelemetry()(&o)
	if len(o.dialOptions) != before+1 {
		t.Errorf("WithOpenTelemetry should append exactly one dial option; got %d → %d", before, len(o.dialOptions))
	}
}
