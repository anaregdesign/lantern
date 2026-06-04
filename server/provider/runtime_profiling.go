package provider

import (
	"log/slog"
	"runtime"
)

// ApplyRuntimeProfiling installs the process-wide mutex- and block-profile
// sampling rates derived from ObservabilityConfig. Both knobs are runtime
// globals (runtime.SetMutexProfileFraction / runtime.SetBlockProfileRate)
// and have no effect unless the matching pprof handlers are reachable —
// typically via LANTERN_PPROF_ENABLED (#238).
//
// Zero leaves the runtime untouched so a process started without these env
// vars behaves exactly as before this hook existed. Non-zero values are
// logged at info so operators can confirm their env actually took effect
// (a common foot-gun: setting the env on the wrong unit or in the wrong
// shell scope).
func ApplyRuntimeProfiling(o ObservabilityConfig, logger *slog.Logger) {
	if o.MutexProfileFraction > 0 {
		prev := runtime.SetMutexProfileFraction(o.MutexProfileFraction)
		if logger != nil {
			logger.Info("mutex profiling enabled",
				slog.Int("fraction", o.MutexProfileFraction),
				slog.Int("previous", prev),
			)
		}
	}
	if o.BlockProfileRate > 0 {
		runtime.SetBlockProfileRate(o.BlockProfileRate)
		if logger != nil {
			logger.Info("block profiling enabled",
				slog.Int("rate_ns", o.BlockProfileRate),
			)
		}
	}
}
