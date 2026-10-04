package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type changeTestSender func(*pb.WatchChangesResponse) error

func (send changeTestSender) Send(frame *pb.WatchChangesResponse) error { return send(frame) }

func TestChangesBootstrapRetentionGapAndAdmission(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 1})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{1}, hlc.Options{})
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithReplication(log, clock, nil).WithDataNamespace()
	options := ChangeServiceOptions{CursorKeys: []ChangeCursorKey{{Version: 1, Key: [32]byte{1}}}, CurrentKeyVersion: 1}
	handler, err := NewChangeConnectHandler(svc, options)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("test sender stopped")
	var checkpoint []byte
	err = handler.watch(t.Context(), &pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Bootstrap: true}, changeTestSender(func(frame *pb.WatchChangesResponse) error {
		if !frame.Bootstrap || len(frame.Invalidations) != 0 {
			t.Fatal("bootstrap was not an empty checkpoint", frame)
		}
		checkpoint = append([]byte(nil), frame.Cursor...)
		return stop
	}))
	if !errors.Is(err, stop) || len(checkpoint) == 0 {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		m := &pb.Mutation{Origin: append([]byte{1}, bytes.Repeat([]byte{0}, 15)...), Seq: uint64(i), Hlc: &pb.HLCTimestamp{NodeId: append([]byte{1}, bytes.Repeat([]byte{0}, 15)...)}, Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"data:orders:1"}}}}}
		if _, err := log.Append(m, clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	err = handler.watch(t.Context(), &pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Cursor: checkpoint}, changeTestSender(func(*pb.WatchChangesResponse) error { t.Fatal("gapped resume sent data"); return stop }))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || err.Error() != changeGapError().Error() {
		t.Fatal("gap leaked origin progress", err)
	}
	for _, req := range []*pb.WatchChangesRequest{{}, {Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Bootstrap: true, Cursor: checkpoint}, {Projection: 99, Bootstrap: true}, {Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Prefix: string([]byte{0xff}), Bootstrap: true}} {
		if err := handler.watch(t.Context(), req, changeTestSender(func(*pb.WatchChangesResponse) error { return stop })); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("invalid change request", err)
		}
	}
	svc.WithDataAuthorization(time.Now)
	if err := handler.watch(context.Background(), &pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Bootstrap: true}, changeTestSender(func(*pb.WatchChangesResponse) error { t.Fatal("anonymous protected CDC"); return stop })); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal(err)
	}
	if _, err := NewChangeConnectHandler(NewLanternService(svc.cache), options); err == nil {
		t.Fatal("unnamespaced CDC enabled")
	}
}

type changeLifecycleMetrics struct{ started, ended, dropped int }

func (m *changeLifecycleMetrics) OnSubscribeStarted()       { m.started++ }
func (m *changeLifecycleMetrics) OnSubscribeEnded()         { m.ended++ }
func (m *changeLifecycleMetrics) OnSubscribeDropped(string) { m.dropped++ }
func TestChangesMetricsOnlyCountAcceptedRegisteredStreams(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 2})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{1}, hlc.Options{})
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithReplication(log, clock, nil).WithDataNamespace()
	metrics := &changeLifecycleMetrics{}
	h, err := NewChangeConnectHandler(svc, ChangeServiceOptions{CursorKeys: []ChangeCursorKey{{Version: 1, Key: [32]byte{1}}}, CurrentKeyVersion: 1, Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.watch(t.Context(), &pb.WatchChangesRequest{}, changeTestSender(func(*pb.WatchChangesResponse) error { t.Fatal("invalid stream send"); return nil })); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	if metrics.started != 0 || metrics.ended != 0 {
		t.Fatal("rejected stream counted")
	}
	stop := errors.New("send failed")
	if err := h.watch(t.Context(), &pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Bootstrap: true}, changeTestSender(func(*pb.WatchChangesResponse) error { return stop })); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if metrics.started != 1 || metrics.ended != 1 || metrics.dropped != 1 {
		t.Fatal("unbalanced failed sender", metrics)
	}
	ctx, cancel := context.WithCancel(t.Context())
	err = h.watch(ctx, &pb.WatchChangesRequest{Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY, Bootstrap: true}, changeTestSender(func(*pb.WatchChangesResponse) error { cancel(); return ctx.Err() }))
	if !errors.Is(err, context.Canceled) || metrics.started != 2 || metrics.ended != 2 || metrics.dropped != 1 {
		t.Fatal("normal cancellation counted as failure", metrics, err)
	}
}
