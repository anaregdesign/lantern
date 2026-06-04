package provider

import (
	"net/http"
	"net/http/pprof"
)

// registerPprofHandlers mounts net/http/pprof handlers on the supplied mux.
// Gated upstream by ObservabilityConfig.EnablePprof (LANTERN_PPROF_ENABLED).
//
// Important: pprof exposes process-internal data (goroutine stacks, heap
// allocations, CPU profile, mutex/block samples). The metrics listener it
// shares is intended for internal scrape traffic only. Operators MUST keep
// the metrics port unreachable from untrusted networks before enabling pprof.
//
// mutex and block profiles require runtime.SetMutexProfileFraction and
// runtime.SetBlockProfileRate to be non-zero respectively; otherwise those
// endpoints return empty samples. Those knobs are wired separately.
func registerPprofHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// runtime profiles are served via pprof.Handler(name).ServeHTTP and the
	// index page links to each by name; explicit registration keeps each
	// profile reachable even without crawling the index.
	for _, name := range []string{"goroutine", "heap", "allocs", "threadcreate", "block", "mutex"} {
		mux.Handle("/debug/pprof/"+name, pprof.Handler(name))
	}
}
