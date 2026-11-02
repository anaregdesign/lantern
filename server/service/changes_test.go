package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type changeTransportMetrics struct {
	started atomic.Int32
	ended   atomic.Int32
	dropped atomic.Int32
	done    chan struct{}
}

func (m *changeTransportMetrics) OnSubscribeStarted() { m.started.Add(1) }
func (m *changeTransportMetrics) OnSubscribeEnded() {
	m.ended.Add(1)
	close(m.done)
}
func (m *changeTransportMetrics) OnSubscribeDropped(string) { m.dropped.Add(1) }

func TestChangeConnectHandlerH2C(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewLanternService(cache).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil).WithDataNamespace()
	metrics := &changeTransportMetrics{done: make(chan struct{})}
	options := ChangeServiceOptions{
		CursorKeys:        []ChangeCursorKey{{Version: 1, Key: [32]byte{1}}},
		CurrentKeyVersion: 1,
		Metrics:           metrics,
	}
	for _, tc := range []struct {
		name    string
		svc     *LanternService
		options ChangeServiceOptions
	}{
		{"nil executor", nil, options},
		{"unnamespaced executor", NewLanternService(cache).WithReplication(log, hlc.New(hlc.NodeID{2}, hlc.Options{}), nil), options},
		{"missing cursor keys", svc, ChangeServiceOptions{}},
		{"invalid heartbeat", svc, ChangeServiceOptions{CursorKeys: options.CursorKeys, CurrentKeyVersion: 1, Heartbeat: time.Second * 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if handler, err := NewChangeConnectHandler(tc.svc, tc.options); err == nil || handler != nil {
				t.Fatalf("invalid CDC construction = %+v, %v", handler, err)
			}
		})
	}
	handler, err := NewChangeConnectHandler(svc, options)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternChangeServiceHandler(handler))
	server := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	clientProtocols := new(http.Protocols)
	clientProtocols.SetUnencryptedHTTP2(true)
	clientHTTP := &http.Client{Transport: &http.Transport{Protocols: clientProtocols}}
	t.Cleanup(clientHTTP.CloseIdleConnections)
	client := graphv1connect.NewLanternChangeServiceClient(clientHTTP, server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	invalid, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{}))
	if err == nil {
		if invalid.Receive() {
			t.Fatal("invalid CDC request returned a frame")
		}
		err = invalid.Err()
		_ = invalid.Close()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument || metrics.started.Load() != 0 || metrics.ended.Load() != 0 {
		t.Fatalf("invalid CDC forwarding = %v, started=%d ended=%d", err, metrics.started.Load(), metrics.ended.Load())
	}
	stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{
		Projection: pb.ChangeProjection_CHANGE_PROJECTION_VALUE,
		Prefix:     "orders:",
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if !stream.Receive() || !stream.Msg().GetBootstrap() || len(stream.Msg().GetCursor()) == 0 || len(stream.Msg().GetInvalidations()) != 0 {
		t.Fatalf("bootstrap forwarding = %+v, %v", stream.Msg(), stream.Err())
	}
	if _, err := svc.Public().PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "orders:1", Value: &pb.Vertex_String_{String_: "ready"},
	}}); err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatal("missing CDC invalidation", stream.Err())
	}
	frame := stream.Msg()
	if frame.GetBootstrap() || len(frame.GetCursor()) == 0 || len(frame.GetInvalidations()) != 1 ||
		frame.GetInvalidations()[0].GetVertexKey() != "orders:1" ||
		frame.GetInvalidations()[0].GetVertex().GetKey() != "orders:1" ||
		frame.GetInvalidations()[0].GetVertex().GetString_() != "ready" {
		t.Fatalf("CDC logical value forwarding = %+v", frame)
	}
	cancel()
	if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodeCanceled {
		t.Fatalf("CDC cancellation forwarding = %v", stream.Err())
	}
	select {
	case <-metrics.done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled transport retained the runtime subscription")
	}
	if metrics.started.Load() != 1 || metrics.ended.Load() != 1 || metrics.dropped.Load() != 0 {
		t.Fatalf("CDC lifetime metrics = started %d ended %d dropped %d", metrics.started.Load(), metrics.ended.Load(), metrics.dropped.Load())
	}
}
