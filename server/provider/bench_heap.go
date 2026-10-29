//go:build lantern_bench_heap && (linux || darwin)

package provider

import "github.com/anaregdesign/lantern/server/service"

// Install before Run, outside ProtectPeerHTTPHandler so membership watcher and
// transport deadline cleanup are included. Maintained Subscribe is classified
// separately; an active stream is never claimed to be globally idle.
func (p *PeerPlaneServer) ObserveBenchHeap(observer *service.BenchHeapObserver) {
	if p != nil && p.server != nil {
		p.server.Handler = observer.WrapPeer(p.server.Handler)
	}
}
