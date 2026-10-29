//go:build lantern_bench_heap && (linux || darwin)

package service

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/anaregdesign/lantern/server/benchheap"
)

type benchHeapCounter struct {
	mu    sync.Mutex
	value benchheap.Counter
}

func (c *benchHeapCounter) enter() func() {
	c.mu.Lock()
	c.value.Begun++
	c.value.Active++
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.value.Completed++
		c.value.Active--
		c.mu.Unlock()
	}
}
func (c *benchHeapCounter) snapshot() benchheap.Counter {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// One tagged App owns observation in this process. No registry or observation
// history is installed in a normal executable. Binding never grants authority.
type BenchHeapObserver struct {
	service                                 *LanternService
	public, peerUnary, peerSubscribe, apply benchHeapCounter
}

var benchHeapBinding atomic.Pointer[BenchHeapObserver]

func NewBenchHeapObserver(s *LanternService) (*BenchHeapObserver, error) {
	if s == nil {
		return nil, errors.New("missing heap observation service")
	}
	o := &BenchHeapObserver{service: s}
	if !benchHeapBinding.CompareAndSwap(nil, o) {
		return nil, errors.New("heap observation already bound")
	}
	return o, nil
}
func (o *BenchHeapObserver) Close() error {
	if !benchheap.CounterIdle(o.public.snapshot()) || !benchheap.CounterIdle(o.peerUnary.snapshot()) || !benchheap.CounterIdle(o.peerSubscribe.snapshot()) || !benchheap.CounterIdle(o.apply.snapshot()) {
		return errors.New("heap observation work remains active")
	}
	if !benchHeapBinding.CompareAndSwap(o, nil) {
		return errors.New("heap observation binding changed")
	}
	return nil
}
func benchHeapApply(s *LanternService) func() {
	if observer := benchHeapBinding.Load(); observer != nil && observer.service == s {
		return observer.apply.enter()
	}
	return func() {}
}
func (o *BenchHeapObserver) WrapPublic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leave := o.public.enter()
		defer leave()
		next.ServeHTTP(w, r)
	})
}
func (o *BenchHeapObserver) WrapPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter := &o.peerUnary
		if r.URL.Path == "/graph.v1.LanternReplicationService/Subscribe" {
			counter = &o.peerSubscribe
		}
		leave := counter.enter()
		defer leave()
		next.ServeHTTP(w, r)
	})
}
func (s *LanternServer) ObserveBenchHeap(o *BenchHeapObserver) error {
	if s == nil || s.server == nil || s.server.Handler == nil || o == nil {
		return errors.New("missing public heap observation handler")
	}
	s.server.Handler = o.WrapPublic(s.server.Handler)
	return nil
}

// Snapshot takes existing locks in the status-read order, then calls only
// direct primitives. No recursive public status wrapper, GC or file I/O occurs
// under the cut. TryRLock allows the caller's finite observation budget.
func (o *BenchHeapObserver) Snapshot(ctx context.Context) (benchheap.Native, error) {
	s := o.service
	if ctx.Err() != nil {
		return benchheap.Native{}, ctx.Err()
	}
	if !s.snapshotReadCutMu.TryRLock() {
		return benchheap.Native{}, benchheap.ErrBusy
	}
	defer s.snapshotReadCutMu.RUnlock()
	if !s.replicationCutMu.TryRLock() {
		return benchheap.Native{}, benchheap.ErrBusy
	}
	defer s.replicationCutMu.RUnlock()
	n := benchheap.Native{Generation: s.replicationCutMu.generation, Faulted: s.publicGraphFaultLocked(), PendingCount: s.pendingCount, PendingBytes: s.pendingBytes,
		PendingLocal: s.pendingLocalMutation != nil, SnapshotActive: s.activeSnapshotInstall.Load() != nil,
		Public: o.public.snapshot(), PeerUnary: o.peerUnary.snapshot(), PeerSubscribe: o.peerSubscribe.snapshot(), Apply: o.apply.snapshot()}
	if s.cache != nil {
		n.Vertices = s.cache.VertexCount()
		n.Edges = s.cache.EdgeCount()
	}
	origins := s.OriginStates()
	if len(origins) > benchheap.MaxOrigins {
		return n, errors.New("heap observation origin bound")
	}
	for _, row := range origins {
		n.Origins = append(n.Origins, benchheap.Origin{ID: hex.EncodeToString(row.Origin[:]), Sequence: row.LastSeq, WallNS: row.LastHLC.WallNs, Logical: row.LastHLC.Logical})
	}
	if s.log != nil {
		n.LogSequence, _ = s.log.LastSeq()
		n.LogLength = s.log.Len()
		n.LogCapacity = s.log.Cap()
		n.LogEvicted = s.log.Evicted()
	}
	if s.receiptStore != nil {
		stats := s.receiptStore.Stats()
		n.ReceiptEntries = stats.Entries
		n.ReceiptBytes = stats.Bytes
		n.ReceiptHighWaterMS = stats.HighWaterMillis
		n.ReceiptOldestDeadlineMS = stats.OldestDeadlineMillis
	}
	return n, nil
}
