package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/proto"
)

type snapshotStageGateApply struct {
	*service.LanternService
	begins, marks, mutations   atomic.Int32
	finishes, verifiedFinishes atomic.Int32
	beforeCheckedBegin         func()
	beforeCheckedFinish        func()
}

func (a *snapshotStageGateApply) BeginSnapshotInstall() (func(bool), error) {
	a.begins.Add(1)
	return a.LanternService.BeginSnapshotInstall()
}

func (a *snapshotStageGateApply) BeginSnapshotInstallChecked(ctx context.Context, check func() error) (func(bool) error, error) {
	a.begins.Add(1)
	if a.beforeCheckedBegin != nil {
		a.beforeCheckedBegin()
	}
	finish, err := a.LanternService.BeginSnapshotInstallChecked(ctx, check)
	if err != nil {
		return nil, err
	}
	return func(verified bool) error {
		if verified && a.beforeCheckedFinish != nil {
			a.beforeCheckedFinish()
		}
		err := finish(verified)
		a.finishes.Add(1)
		if verified && err == nil {
			a.verifiedFinishes.Add(1)
		}
		return err
	}, nil
}

func (a *snapshotStageGateApply) ApplySnapshotWatermarks(cut map[string]uint64, ts hlc.Timestamp) error {
	a.marks.Add(1)
	return a.LanternService.ApplySnapshotWatermarks(cut, ts)
}

func (a *snapshotStageGateApply) ApplyMutation(ctx context.Context, mutation *pb.Mutation) error {
	a.mutations.Add(1)
	return a.LanternService.ApplyMutation(ctx, mutation)
}

type snapshotStageGateGraph struct {
	*graphcache.GraphCache[string, *pb.Vertex]
	indexBegins, indexFinishes atomic.Int32
}

func (g *snapshotStageGateGraph) BeginSearchIndexRecovery() {
	g.indexBegins.Add(1)
	g.GraphCache.BeginSearchIndexRecovery()
}

func (g *snapshotStageGateGraph) CompleteSearchIndexRecovery() error {
	g.indexFinishes.Add(1)
	return g.GraphCache.CompleteSearchIndexRecovery()
}

type snapshotStageGateResult struct {
	err, contextErr, terminal error
	header                    *pb.SnapshotHeader
	footer                    *pb.SnapshotFooter
	vertices                  int
	transportBytes            uint64
	completeEOF               bool
}

type snapshotStageGateInstaller struct {
	replication.SnapshotInstaller
	limits   replication.SnapshotTransportLimits
	afterEOF func()
	stop     context.CancelFunc
	headers  chan struct{}
	results  chan snapshotStageGateResult
}

func (i *snapshotStageGateInstaller) SnapshotTransportLimits() replication.SnapshotTransportLimits {
	return i.limits
}

func (i *snapshotStageGateInstaller) Install(ctx context.Context, stream replication.SnapshotStream) (replication.SnapshotInstallResult, error) {
	observed := &snapshotStageGateStream{SnapshotStream: stream, afterEOF: i.afterEOF, headers: i.headers}
	result, err := i.SnapshotInstaller.Install(ctx, observed)
	observed.result.err, observed.result.contextErr = err, ctx.Err()
	// Stop after this one attempt, without renewing its context or opening a
	// second Snapshot whose admission could hide the first request's expiry.
	i.stop()
	i.results <- observed.result
	return result, err
}

type snapshotStageGateStream struct {
	replication.SnapshotStream
	afterEOF func()
	headers  chan<- struct{}
	result   snapshotStageGateResult
}

func (s *snapshotStageGateStream) Receive() bool {
	if !s.SnapshotStream.Receive() {
		s.result.terminal = s.Err()
		if s.result.terminal == nil || errors.Is(s.result.terminal, io.EOF) {
			s.result.completeEOF = true
			if s.afterEOF != nil {
				s.afterEOF()
			}
		}
		return false
	}
	frame := s.Msg()
	s.result.transportBytes += uint64(proto.Size(frame))
	switch {
	case frame.GetHeader() != nil:
		s.result.header = proto.Clone(frame.GetHeader()).(*pb.SnapshotHeader)
		s.headers <- struct{}{}
	case frame.GetFooter() != nil:
		s.result.footer = proto.Clone(frame.GetFooter()).(*pb.SnapshotFooter)
	case frame.GetVertex() != nil:
		s.result.vertices++
	}
	return true
}

type snapshotStageGatePeer struct {
	graphv1connect.LanternReplicationServiceHandler
	source             *service.LanternReplicationService
	block              bool
	held               chan struct{}
	stopped            chan error
	snapshots, dropped atomic.Int32
}

func (p *snapshotStageGatePeer) Snapshot(ctx context.Context, request *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	p.snapshots.Add(1)
	ctx = service.WithReplicationTailMetadata(ctx, request.Header().Values(service.SnapshotTailHeader), stream.ResponseHeader())
	err := p.source.Snapshot(ctx, request.Msg, snapshotStageGateSender{ctx: ctx, stream: stream, peer: p})
	select {
	case p.stopped <- err:
	default:
		p.dropped.Add(1)
	}
	return err
}

// This terminal stream proves only that an existing disk owner can be
// reacquired. It carries no native admission and never supplies a live image.
type snapshotStageGateTerminalStream struct {
	err      error
	receives int
}

func (s *snapshotStageGateTerminalStream) Receive() bool {
	s.receives++
	return false
}
func (*snapshotStageGateTerminalStream) Msg() *pb.SnapshotResponse { return nil }
func (s *snapshotStageGateTerminalStream) Err() error              { return s.err }

type snapshotStageGateSender struct {
	ctx    context.Context
	stream *connect.ServerStream[pb.SnapshotResponse]
	peer   *snapshotStageGatePeer
}

func (s snapshotStageGateSender) Send(frame *pb.SnapshotResponse) error {
	if err := s.stream.Send(frame); err != nil {
		return err
	}
	if s.peer.block && frame.GetHeader() != nil {
		select {
		case s.peer.held <- struct{}{}:
		default:
			s.peer.dropped.Add(1)
		}
		<-s.ctx.Done()
		return s.ctx.Err()
	}
	return nil
}

// These controlled observations exercise real signed membership, workload TLS,
// response admission, Snapshot framing and the staged installer together. They
// are mechanism tests, not configured-source or sustained-load qualification.
func TestSnapshotStageNativeAdmissionAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		expireEOF    bool
		expireBegin  bool
		expireFinish bool
		priorFault   bool
	}{
		{name: "original admission expires after complete wire EOF", expireEOF: true},
		{name: "checked service entry rejects expired original admission", expireBegin: true},
		{name: "checked service finish refuses expired original admission", expireFinish: true},
		{name: "cancel while native body is blocked"},
		{name: "cancel preserves a preexisting repair fault", priorFault: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expire := tc.expireEOF || tc.expireBegin || tc.expireFinish
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			runCtx, stop := context.WithCancel(ctx)
			defer stop()
			var clockMu sync.Mutex
			low := time.Now().UTC().Add(2 * time.Second)
			high, elapsed, owner := low, time.Duration(0), new(byte)
			bounds := func() (time.Time, time.Time, error) {
				clockMu.Lock()
				defer clockMu.Unlock()
				return low, high, nil
			}
			observe := func() (provider.PeerTimeObservation, error) {
				clockMu.Lock()
				defer clockMu.Unlock()
				return peerWireObservation{owner, low, high, elapsed}, nil
			}
			var graphs [2]*graphcache.GraphCache[string, *pb.Vertex]
			var peer *snapshotStageGatePeer
			fixture := newPeerSecurityFixture(t, peerSecurityOptions{
				Now:        func() time.Time { _, current, _ := bounds(); return current },
				TimeBounds: bounds, Time: observe, IssuedBefore: time.Minute,
				LogOptions: func(int) mutationlog.Options { return mutationlog.Options{Capacity: 4} },
				Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
					graphs[index] = runtime.GraphCache()
					if index != 0 {
						return
					}
					source, err := runtime.NewLanternReplicationService(f.primary[index])
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(source.CloseSnapshotTails)
					peer = &snapshotStageGatePeer{
						LanternReplicationServiceHandler: service.NewLanternReplicationServiceConnectHandler(source),
						source:                           source, block: !expire, held: make(chan struct{}, 1), stopped: make(chan error, 1),
					}
					_, handler := graphv1connect.NewLanternReplicationServiceHandler(peer)
					for _, method := range []string{"PeerStatus", "Snapshot", "Subscribe"} {
						mux.Handle("/graph.v1.LanternReplicationService/"+method, handler)
					}
				},
			})
			put := func(node int, key string) {
				t.Helper()
				if _, err := fixture.primary[node].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
					Key: key, Value: &pb.Vertex_String_{String_: "value"},
				}}); err != nil {
					t.Fatal(err)
				}
			}
			for sequence := range 8 {
				put(0, fmt.Sprintf("data:stage-source-%d", sequence))
			}
			put(1, "data:stage-resident")
			if first, ok := fixture.logs[0].FirstSeq(); !ok || first != 5 {
				t.Fatalf("source retained first=%d present=%t, want an actual initial gap", first, ok)
			}
			if tc.priorFault {
				finish, err := fixture.primary[1].BeginSnapshotInstall()
				if err != nil {
					t.Fatal(err)
				}
				finish(false)
			}
			beforeGraph, beforeOrigins := graphs[1].SnapshotReplication(), fixture.primary[1].OriginStates()
			beforeSeq, beforeHasSeq := fixture.logs[1].LastSeq()
			apply := &snapshotStageGateApply{LanternService: fixture.primary[1]}
			graph := &snapshotStageGateGraph{GraphCache: graphs[1]}
			spool := graphSnapshotSpoolConfig(t)
			delegate := replication.NewGraphOnlySnapshotInstaller(apply, graph, spool)
			bounded, ok := delegate.(interface {
				SnapshotTransportLimits() replication.SnapshotTransportLimits
			})
			if !ok {
				t.Fatal("graph-only installer lost its bounded wire codec")
			}
			installer := &snapshotStageGateInstaller{
				SnapshotInstaller: delegate, limits: bounded.SnapshotTransportLimits(), stop: stop,
				headers: make(chan struct{}, 1), results: make(chan snapshotStageGateResult, 1),
			}
			expireOriginal := func() {
				clockMu.Lock()
				defer clockMu.Unlock()
				low, high = low.Add(31*time.Second), high.Add(31*time.Second)
				elapsed += 31 * time.Second
			}
			if tc.expireEOF {
				installer.afterEOF = expireOriginal
			}
			// These hooks preserve the exact checker passed by the native
			// installer. They test the service bridge, not an actual gate wait;
			// the blocking-gate interleaving is covered in service unit tests.
			if tc.expireBegin {
				apply.beforeCheckedBegin = expireOriginal
			}
			if tc.expireFinish {
				apply.beforeCheckedFinish = expireOriginal
			}
			transport, err := provider.NewWorkloadPeerTransport(fixture.runtimes[1])
			if err != nil {
				t.Fatal(err)
			}
			pump := replication.NewPump(replication.Config{SnapshotSpool: spool,
				NodeID: hlc.NodeID{4}, NamespaceFormat: "namespaced-v1", Peers: []string{fixture.servers[0].URL},
				PeerTransport: transport, SnapshotInstaller: installer,
				BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
			}, apply, graph)
			done := make(chan error, 1)
			go func() { done <- pump.Run(runCtx) }()
			joined := false
			defer func() {
				stop()
				if !joined {
					select {
					case err := <-done:
						if err != nil {
							t.Error(err)
						}
					case <-ctx.Done():
						t.Error("native staging Pump did not join", ctx.Err())
					}
				}
			}()
			if !expire {
				select {
				case <-peer.held:
				case <-ctx.Done():
					t.Fatal("source did not block its actual native body", ctx.Err())
				}
				select {
				case <-installer.headers:
				case <-ctx.Done():
					t.Fatal("receiver did not consume the native Header", ctx.Err())
				}
				if apply.begins.Load() != 0 || graph.indexBegins.Load() != 0 || fixture.primary[1].SnapshotInstallActive() {
					t.Fatal("blocked body entered live publication")
				}
				stop()
			}
			var observed snapshotStageGateResult
			select {
			case observed = <-installer.results:
			case <-ctx.Done():
				t.Fatal("native staged install did not return", ctx.Err())
			}
			if expire {
				if !observed.completeEOF || observed.header == nil || observed.header.GetCutoffLocalSeq() != 8 ||
					observed.footer == nil || observed.footer.GetVertexCount() != 8 || observed.vertices != 8 || observed.contextErr != nil {
					t.Fatalf("incomplete source or caller cancellation hid original admission check: %+v", observed)
				}
				if observed.err == nil || !strings.Contains(observed.err.Error(), "peer membership is invalid or unavailable") {
					t.Fatalf("expired original native admission = %v", observed.err)
				}
			} else if observed.err == nil || observed.completeEOF || !errors.Is(observed.contextErr, context.Canceled) {
				t.Fatalf("blocked native cancellation = %+v", observed)
			}
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal("native staging Pump failed to join", err)
				}
			case <-ctx.Done():
				t.Fatal("native staging Pump did not join", ctx.Err())
			}
			select {
			case err := <-peer.stopped:
				if expire && err != nil {
					t.Fatal("source failed before its complete EOF", err)
				}
			case <-ctx.Done():
				t.Fatal("native source handler did not join", ctx.Err())
			}
			if peer.snapshots.Load() != 1 || peer.dropped.Load() != 0 {
				t.Fatalf("native case retried or lost its initial completion: snapshots=%d dropped=%d", peer.snapshots.Load(), peer.dropped.Load())
			}
			fresh := graphv1connect.NewLanternReplicationServiceClient(fixture.runtimes[1].HTTPClient(), fixture.servers[0].URL)
			status, err := fresh.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"}))
			if err != nil || len(status.Msg.GetOrigins()) != 1 || status.Msg.GetOrigins()[0].GetLastSeq() != 8 {
				t.Fatalf("fresh native admission after rejected Snapshot = %v, %v", status, err)
			}
			var wantBegin, wantReplay int32
			if tc.expireBegin || tc.expireFinish {
				wantBegin = 1
			}
			if tc.expireFinish {
				wantReplay = 1
			}
			if apply.begins.Load() != wantBegin || apply.marks.Load() != wantReplay || apply.mutations.Load() != 0 ||
				graph.indexBegins.Load() != wantReplay || graph.indexFinishes.Load() != wantReplay ||
				apply.finishes.Load() != wantReplay || apply.verifiedFinishes.Load() != 0 {
				t.Fatalf("native guard bridge owners: begin=%d marks=%d mutations=%d index=%d/%d finish=%d verified=%d, want begin=%d replay=%d and no verified finish",
					apply.begins.Load(), apply.marks.Load(), apply.mutations.Load(), graph.indexBegins.Load(), graph.indexFinishes.Load(),
					apply.finishes.Load(), apply.verifiedFinishes.Load(), wantBegin, wantReplay)
			}
			if afterSeq, afterHasSeq := fixture.logs[1].LastSeq(); afterSeq != beforeSeq || afterHasSeq != beforeHasSeq {
				t.Fatal("rejected install changed publication log")
			}
			if !tc.expireFinish {
				if !reflect.DeepEqual(beforeGraph, graphs[1].SnapshotReplication()) || !reflect.DeepEqual(beforeOrigins, fixture.primary[1].OriginStates()) {
					t.Fatal("pre-publication rejection changed graph or origin watermarks")
				}
			} else {
				// The complete body and watermarks were applied while the
				// original admission was valid. Its later expiry must leave
				// these effects fenced behind the unresolved repair fault.
				if len(graphs[1].SnapshotReplication().Graph.Vertices) != 9 || fixture.primary[1].LocalSeq(hlc.NodeID{3}) != 8 || fixture.primary[1].LocalSeq(hlc.NodeID{4}) != 1 {
					t.Fatal("finish guard case did not reach complete graph/watermark replay")
				}
				for sequence := range 8 {
					key := fmt.Sprintf("data:stage-source-%d", sequence)
					vertex, live := graphs[1].GetVertex(key)
					if !live || vertex.GetString_() != "value" {
						t.Fatalf("finish guard lost staged effect %s", key)
					}
				}
				if resident, live := graphs[1].GetVertex("data:stage-resident"); !live || resident.GetString_() != "value" {
					t.Fatal("finish guard changed the preexisting resident")
				}
			}
			if fixture.primary[1].SnapshotInstallActive() || fixture.primary[1].SnapshotRepairRequired() != (tc.priorFault || tc.expireFinish) {
				t.Fatal("rejected stage changed receiver publication health")
			}
			resident, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:stage-resident"})
			if tc.priorFault || tc.expireFinish {
				if connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("unverified finish or preexisting fault reopened public reads: %v", err)
				}
			} else if err != nil || resident.GetVertex().GetString_() != "value" {
				t.Fatalf("healthy resident became unavailable: %v, %v", resident, err)
			}
			assertGraphSnapshotSpoolReleased(t, spool)
			// A complete source may legitimately retain its original tail for
			// this peer. Do not issue another native Snapshot or disturb that
			// owner. This synthetic terminal input tests receiver disk lease
			// reacquisition only, using the same strategy and directory.
			terminalErr := errors.New("controlled local terminal stream for disk owner reuse")
			terminal := &snapshotStageGateTerminalStream{err: terminalErr}
			vertices := len(graphs[1].SnapshotReplication().Graph.Vertices)
			sourceCursor := fixture.primary[1].LocalSeq(hlc.NodeID{3})
			result, err := delegate.Install(ctx, terminal)
			if err != terminalErr || terminal.receives != 1 || result.Header != nil || result.Graph != (replication.SnapshotGraphCounts{}) {
				t.Fatalf("same receiver disk owner was not reacquired for one terminal Receive: receives=%d result=%+v err=%v", terminal.receives, result, err)
			}
			if apply.begins.Load() != wantBegin || apply.marks.Load() != wantReplay || apply.mutations.Load() != 0 ||
				graph.indexBegins.Load() != wantReplay || graph.indexFinishes.Load() != wantReplay || apply.finishes.Load() != wantReplay ||
				apply.verifiedFinishes.Load() != 0 || len(graphs[1].SnapshotReplication().Graph.Vertices) != vertices ||
				fixture.primary[1].LocalSeq(hlc.NodeID{3}) != sourceCursor || fixture.primary[1].LocalSeq(hlc.NodeID{4}) != 1 ||
				fixture.primary[1].SnapshotInstallActive() || fixture.primary[1].SnapshotRepairRequired() != (tc.priorFault || tc.expireFinish) {
				t.Fatal("synthetic disk-owner check changed native case state")
			}
			assertGraphSnapshotSpoolReleased(t, spool)
		})
	}
}

func TestSnapshotStageNativeSpoolBeyondMemoryBoundary(t *testing.T) {
	qualifiedUTC, began, owner := time.Now().UTC(), time.Now(), new(byte)
	var graphs [2]*graphcache.GraphCache[string, *pb.Vertex]
	var peer *snapshotStageGatePeer
	fixture := newPeerSecurityFixture(t, peerSecurityOptions{
		Now:        func() time.Time { return qualifiedUTC },
		TimeBounds: func() (time.Time, time.Time, error) { return qualifiedUTC, qualifiedUTC, nil },
		Time: func() (provider.PeerTimeObservation, error) {
			return peerWireObservation{owner, qualifiedUTC, qualifiedUTC, time.Since(began)}, nil
		},
		IssuedBefore: time.Minute,
		LogOptions:   func(int) mutationlog.Options { return mutationlog.Options{Capacity: 4} },
		Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
			graphs[index] = runtime.GraphCache()
			if index != 0 {
				return
			}
			source, err := runtime.NewLanternReplicationService(f.primary[index])
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(source.CloseSnapshotTails)
			peer = &snapshotStageGatePeer{
				LanternReplicationServiceHandler: service.NewLanternReplicationServiceConnectHandler(source),
				source:                           source, stopped: make(chan error, 1),
			}
			_, handler := graphv1connect.NewLanternReplicationServiceHandler(peer)
			for _, method := range []string{"PeerStatus", "Snapshot", "Subscribe"} {
				mux.Handle("/graph.v1.LanternReplicationService/"+method, handler)
			}
		},
	})
	// One incompressible payload per message keeps the real native transfer
	// substantial as well as exceeding the old encoded 64 MiB stage limit.
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	seedCtx, seedCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer seedCancel()
	for sequence := range 65 {
		if _, err := fixture.primary[0].PutVertex(seedCtx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: fmt.Sprintf("data:spool-large-%02d", sequence), Value: &pb.Vertex_Bytes{Bytes: payload},
		}}); err != nil {
			t.Fatal("large native source seed", err)
		}
	}
	if _, err := fixture.primary[1].PutVertex(seedCtx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "data:spool-resident", Value: &pb.Vertex_String_{String_: "resident"},
	}}); err != nil {
		t.Fatal(err)
	}
	if first, ok := fixture.logs[0].FirstSeq(); !ok || first != 62 {
		t.Fatalf("large source did not require Snapshot: retained first=%d present=%t", first, ok)
	}
	// Source preparation is outside this bounded native transfer. Its
	// admission is minted by the ordinary transport and is never extended.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	apply := &snapshotStageGateApply{LanternService: fixture.primary[1]}
	graph := &snapshotStageGateGraph{GraphCache: graphs[1]}
	spool := graphSnapshotSpoolConfig(t)
	delegate := replication.NewGraphOnlySnapshotInstaller(apply, graph, spool)
	bounded := delegate.(interface {
		SnapshotTransportLimits() replication.SnapshotTransportLimits
	})
	installer := &snapshotStageGateInstaller{
		SnapshotInstaller: delegate, limits: bounded.SnapshotTransportLimits(), stop: stop,
		headers: make(chan struct{}, 1), results: make(chan snapshotStageGateResult, 1),
	}
	transport, err := provider.NewWorkloadPeerTransport(fixture.runtimes[1])
	if err != nil {
		t.Fatal(err)
	}
	pump := replication.NewPump(replication.Config{
		SnapshotSpool: spool, NodeID: hlc.NodeID{4}, NamespaceFormat: "namespaced-v1", Peers: []string{fixture.servers[0].URL},
		PeerTransport: transport, SnapshotInstaller: installer, BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
	}, apply, graph)
	done := make(chan error, 1)
	go func() { done <- pump.Run(runCtx) }()
	joined := false
	defer func() {
		stop()
		if !joined {
			select {
			case <-done:
			case <-ctx.Done():
				t.Error("large native spool Pump did not join during cleanup", ctx.Err())
			}
		}
	}()
	var observed snapshotStageGateResult
	select {
	case observed = <-installer.results:
	case <-ctx.Done():
		t.Fatal("large native Snapshot did not finish within its original caller budget", ctx.Err())
	}
	if observed.err != nil || observed.contextErr != nil || !observed.completeEOF || observed.header.GetCutoffLocalSeq() != 65 ||
		observed.footer.GetVertexCount() != 65 || observed.vertices != 65 || observed.transportBytes <= 64<<20 {
		t.Fatalf("large native spool result: %+v", observed)
	}
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("large native spool Pump did not join", ctx.Err())
	}
	select {
	case err := <-peer.stopped:
		if err != nil {
			t.Fatal("large native source did not finish normally", err)
		}
	case <-ctx.Done():
		t.Fatal("large native source did not join", ctx.Err())
	}
	if peer.snapshots.Load() != 1 || peer.dropped.Load() != 0 {
		t.Fatalf("large native case retried or lost its completion: snapshots=%d dropped=%d", peer.snapshots.Load(), peer.dropped.Load())
	}
	if apply.begins.Load() != 1 || apply.marks.Load() != 1 || apply.finishes.Load() != 1 || apply.verifiedFinishes.Load() != 1 ||
		graph.indexBegins.Load() != 1 || graph.indexFinishes.Load() != 1 || fixture.primary[1].SnapshotRepairRequired() || fixture.primary[1].SnapshotInstallActive() {
		t.Fatal("large native Snapshot did not have exactly one verified live install")
	}
	for sequence := range 65 {
		key := fmt.Sprintf("data:spool-large-%02d", sequence)
		response, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: key})
		if err != nil || !bytes.Equal(response.GetVertex().GetBytes(), payload) {
			t.Fatalf("large native Snapshot lost exact value %d: %v", sequence, err)
		}
	}
	resident, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:spool-resident"})
	if err != nil || resident.GetVertex().GetString_() != "resident" || fixture.primary[1].LocalSeq(hlc.NodeID{3}) != 65 || fixture.primary[1].LocalSeq(hlc.NodeID{4}) != 1 {
		t.Fatalf("large native Snapshot lost resident or origin cursors: %v, %v", resident, err)
	}
	assertGraphSnapshotSpoolReleased(t, spool)
	t.Logf("native Snapshot accepted %d protobuf payload bytes across 65 frames using explicit %d-byte disk quota; verified once, owners joined; no configured-source or load qualification", observed.transportBytes, spool.MaxDiskBytes)
}
