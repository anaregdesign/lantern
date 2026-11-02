//go:build lantern_bench_heap && (linux || darwin)

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/benchheap"
)

func TestBenchHeapServerObservesCompleteHandlerLifetime(t *testing.T) {
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	observer, err := NewBenchHeapObserver(svc)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = observer.Close()
		}
	})
	for _, tc := range []struct {
		name     string
		server   *LanternServer
		observer *BenchHeapObserver
	}{
		{"nil receiver", nil, observer},
		{"missing HTTP server", &LanternServer{}, observer},
		{"missing handler", &LanternServer{server: &http.Server{}}, observer},
		{"missing observer", &LanternServer{server: &http.Server{Handler: http.NotFoundHandler()}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.server.ObserveBenchHeap(tc.observer); err == nil {
				t.Fatal("incomplete handler observation accepted")
			}
		})
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	server := &LanternServer{server: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer func() { <-release }()
		w.WriteHeader(http.StatusAccepted)
		close(entered)
	})}}
	if err := server.ObserveBenchHeap(observer); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	go func() {
		defer close(done)
		server.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/data", nil))
	}()
	<-entered
	cut, err := observer.Snapshot(context.Background())
	if err != nil || cut.Public.Active != 1 || cut.Public.Begun != 1 || cut.Public.Completed != 0 || benchheap.NativeIdle(cut) {
		t.Fatalf("handler cleanup was not observed: %+v, %v", cut.Public, err)
	}
	if err := observer.Close(); err == nil {
		t.Fatal("observer closed before complete handler return")
	}
	unblock()
	<-done
	cut, err = observer.Snapshot(context.Background())
	if err != nil || !benchheap.NativeIdle(cut) || cut.Public.Begun != 1 || cut.Public.Completed != 1 || response.Code != http.StatusAccepted {
		t.Fatalf("completed handler observation = %+v, status %d, %v", cut.Public, response.Code, err)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
}
