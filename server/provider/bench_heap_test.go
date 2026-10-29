//go:build lantern_bench_heap && (linux || darwin)

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

func TestBenchHeapPeerStreamClassification(t *testing.T) {
	s := service.NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](0))
	o, err := service.NewBenchHeapObserver(s)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	var observed bool
	p := &PeerPlaneServer{server: &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		n, err := o.Snapshot(context.Background())
		if err != nil || n.PeerSubscribe.Active != 1 || n.PeerUnary.Active != 0 {
			t.Error("private stream classification failed", err)
		}
		observed = true
	})}}
	p.ObserveBenchHeap(o)
	p.server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/graph.v1.LanternReplicationService/Subscribe", nil))
	if !observed {
		t.Fatal("peer wrapper bypassed handler")
	}
}
