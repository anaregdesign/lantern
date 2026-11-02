//go:build lantern_bench_heap && (linux || darwin)

package dataruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/server/benchheap"
)

func TestBenchHeapWholeHandlerReturn(t *testing.T) {
	o := &BenchHeapObserver{}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := o.WrapPublic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { defer func() { <-release }(); close(started) }))
	go func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/data", nil)); close(done) }()
	<-started
	if benchheap.CounterIdle(o.public.snapshot()) {
		t.Fatal("handler cleanup was not counted")
	}
	close(release)
	<-done
	if !benchheap.CounterIdle(o.public.snapshot()) {
		t.Fatal("completed handler remained active")
	}
}
func TestBenchHeapNativeCutAndApply(t *testing.T) {
	s := NewExecutor(&fakeBackend{})
	o, err := NewBenchHeapObserver(s)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	leave := benchHeapApply(s)
	n, err := o.Snapshot(context.Background())
	if err != nil || benchheap.NativeIdle(n) {
		t.Fatal("in-flight Apply was not observed", err)
	}
	leave()
	n, err = o.Snapshot(context.Background())
	if err != nil || !benchheap.NativeIdle(n) {
		t.Fatal("completed Apply was not drained", err)
	}
	s.snapshotReadCutMu.Lock()
	_, err = o.Snapshot(context.Background())
	s.snapshotReadCutMu.Unlock()
	if err != benchheap.ErrBusy {
		t.Fatal("snapshot cut failed to respect busy state")
	}
}

func TestBenchHeapCounterConcurrentCutIsCoherent(t *testing.T) {
	var counter benchHeapCounter
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		workers.Go(func() {
			<-start
			for range 1000 {
				leave := counter.enter()
				leave()
			}
		})
	}
	close(start)
	for range 10000 {
		cut := counter.snapshot()
		if cut.Active < 0 || cut.Begun != cut.Completed+uint64(cut.Active) {
			t.Fatalf("torn activity cut: %+v", cut)
		}
	}
	workers.Wait()
	if cut := counter.snapshot(); cut.Begun != 8000 || !benchheap.CounterIdle(cut) {
		t.Fatalf("incomplete handler activity: %+v", cut)
	}
}
