package provider

import (
	"io"
	"log/slog"
	"os"
)

// ServingLogger owns the level used by the serving logger and all descendants.
// It must be retained by pointer; independent owners never share level state.
// Construction uses only the startup configuration. Managed policy adoption is
// unavailable until a verified committed projection adapter is installed.
type ServingLogger struct {
	level           slog.LevelVar
	logger          *slog.Logger
	fromEnvironment bool
}

// NewServingLogger constructs the focused serving log owner for Wire.
func NewServingLogger(o ObservabilityConfig) *ServingLogger {
	return newServingLogger(o, os.Stderr)
}

func newServingLogger(o ObservabilityConfig, w io.Writer) *ServingLogger {
	owner := &ServingLogger{fromEnvironment: o.LogLevelFromEnvironment}
	owner.level.Set(o.LogLevel)
	owner.logger = newLoggerWithLevel(o, w, &owner.level)
	return owner
}

// applyLevel is the private application seam for the future verified policy
// projection. It changes filtering for retained loggers without replacing their
// handlers. Production construction currently sets only the startup level.
func (owner *ServingLogger) applyLevel(level slog.Level) {
	owner.level.Set(level)
}
