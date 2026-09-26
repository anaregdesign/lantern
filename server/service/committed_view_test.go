package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestLanternService_CommittedViewWaitsForLogPublication(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 2})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewLanternService(cache).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: hlc.NodeID{1}}
	graphVisible := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	commitDone := make(chan error, 1)
	go func() {
		svc.replicationCutMu.Lock()
		defer svc.replicationCutMu.Unlock()
		_, err := log.CommitWithPublication(&pb.Mutation{}, stamp, func(mutationlog.Entry) {
			if putErr := cache.PutVertex("committed", &pb.Vertex{Key: "committed"}); putErr != nil {
				panic(putErr)
			}
			close(graphVisible)
			<-release
		})
		commitDone <- err
	}()
	select {
	case <-graphVisible:
	case <-time.After(2 * time.Second):
		t.Fatal("graph callback was not entered")
	}

	// A raw GraphCache observer can already see the callback's graph change,
	// while the log is still publishing. The server-owned view must wait and
	// copy both values under one cut.
	if count := cache.VertexCount(); count != 1 {
		t.Fatalf("graph callback count = %d, want 1", count)
	}
	viewStarted := make(chan struct{})
	viewEntered := make(chan struct{})
	type observation struct{ graph, log uint64 }
	viewDone := make(chan observation, 1)
	go func() {
		close(viewStarted)
		var seen observation
		if err := svc.withCommittedView(func() error {
			close(viewEntered)
			seen.graph = uint64(cache.VertexCount())
			seen.log, _ = log.LastSeq()
			return nil
		}); err != nil {
			viewDone <- observation{}
			return
		}
		viewDone <- seen
	}()
	statusStarted := make(chan struct{})
	type statusResult struct {
		count uint64
		err   error
	}
	statusDone := make(chan statusResult, 1)
	go func() {
		close(statusStarted)
		resp, err := svc.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			statusDone <- statusResult{err: err}
			return
		}
		statusDone <- statusResult{count: resp.GetVertexCount()}
	}()
	<-viewStarted
	<-statusStarted
	select {
	case <-viewEntered:
		t.Fatal("committed view entered during callback publication")
	case early := <-statusDone:
		t.Fatalf("direct status escaped callback window: %+v", early)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-commitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("log publication did not finish")
	}
	select {
	case seen := <-viewDone:
		if seen != (observation{graph: 1, log: 1}) {
			t.Fatalf("committed view = %+v, want graph/log seq 1", seen)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("committed view did not finish")
	}
	select {
	case status := <-statusDone:
		if status.err != nil || status.count != 1 {
			t.Fatalf("direct status after publication = %+v, want count 1", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("direct status did not finish")
	}
}

func TestLanternService_CommittedViewFailsClosed(t *testing.T) {
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	release, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := svc.withCommittedView(func() error { called = true; return nil }); connect.CodeOf(err) != connect.CodeFailedPrecondition || called {
		t.Fatalf("faulted committed view = (%v, called=%v), want failed precondition without callback", err, called)
	}
	if _, err := svc.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("direct status during incomplete Snapshot install = %v, want failed precondition", err)
	}
	release(true)
	callbackErr := errors.New("capture failed")
	if err := svc.withCommittedView(func() error { return callbackErr }); !errors.Is(err, callbackErr) {
		t.Fatalf("capture error = %v, want original error", err)
	}
}

type heldVertexReadBackend struct {
	Backend
	entered chan struct{}
	release <-chan struct{}
}

func (b *heldVertexReadBackend) GetVertex(key string) (*pb.Vertex, bool) {
	close(b.entered)
	<-b.release
	return b.Backend.GetVertex(key)
}

func TestLanternService_GetVertexBlocksSnapshotAdmissionWhileReading(t *testing.T) {
	fb := newFakeBackend()
	source := &pb.Vertex{Key: "kept", Value: &pb.Vertex_String_{String_: "before"}}
	fb.vertices["kept"] = source
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	held := &heldVertexReadBackend{
		Backend: fb, entered: make(chan struct{}), release: release,
	}
	svc := NewLanternService(held)
	type readResult struct {
		vertex *pb.Vertex
		err    error
	}
	readDone := make(chan readResult, 1)
	go func() {
		resp, err := svc.GetVertex(context.Background(), &pb.GetVertexRequest{Key: "kept"})
		readDone <- readResult{vertex: resp.GetVertex(), err: err}
	}()
	select {
	case <-held.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("public GetVertex did not enter backend")
	}
	type installResult struct {
		finish func(bool)
		err    error
	}
	installStarted := make(chan struct{})
	installDone := make(chan installResult, 1)
	go func() {
		close(installStarted)
		finish, err := svc.BeginSnapshotInstall()
		installDone <- installResult{finish, err}
	}()
	<-installStarted
	select {
	case result := <-installDone:
		if result.finish != nil {
			result.finish(false)
		}
		t.Fatalf("Snapshot install entered during a public GraphCache read: %v", result.err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	var observed *pb.Vertex
	select {
	case result := <-readDone:
		if result.err != nil {
			t.Fatalf("in-flight public GetVertex: %v", result.err)
		}
		observed = result.vertex
	case <-time.After(2 * time.Second):
		t.Fatal("public GetVertex did not complete")
	}
	select {
	case result := <-installDone:
		if result.err != nil {
			t.Fatal(result.err)
		}
		defer result.finish(false)
	case <-time.After(2 * time.Second):
		t.Fatal("Snapshot install did not start after the public read")
	}
	source.Value = &pb.Vertex_String_{String_: "during-replay"}
	if observed == source || observed.GetString_() != "before" {
		t.Fatalf("in-flight read returned a mutable cache alias: %v", observed)
	}
	if _, err := svc.GetVertex(context.Background(), &pb.GetVertexRequest{Key: "kept"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("public GetVertex after install admission = %v, want FailedPrecondition", err)
	}
}

func TestLanternService_VerifiedSnapshotFinishWaitsForReadLatch(t *testing.T) {
	fb := newFakeBackend()
	fb.vertices["kept"] = &pb.Vertex{Key: "kept"}
	svc := NewLanternService(fb)
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	// A public read cannot enter while faulted, so hold its admission latch
	// directly to exercise a verified retry racing the end of a read.
	svc.snapshotReadCutMu.RLock()
	held := true
	t.Cleanup(func() {
		if held {
			svc.snapshotReadCutMu.RUnlock()
		}
	})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		finish(true)
		close(done)
	}()
	<-started
	select {
	case <-done:
		t.Fatal("verified finish cleared the fault while a read held the latch")
	case <-time.After(25 * time.Millisecond):
	}
	captured := false
	if err := svc.withCommittedView(func() error { captured = true; return nil }); connect.CodeOf(err) != connect.CodeFailedPrecondition || captured {
		t.Fatalf("finish during held read exposed a healthy publication: %v (captured=%t)", err, captured)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := svc.GetVertex(context.Background(), &pb.GetVertexRequest{Key: "kept"})
		readDone <- err
	}()
	earlyRead := false
	select {
	case err := <-readDone:
		earlyRead = true
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("public read before verified finish = %v, want FailedPrecondition", err)
		}
	case <-time.After(25 * time.Millisecond):
	}
	svc.snapshotReadCutMu.RUnlock()
	held = false
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("verified finish did not clear the fault after the read released its latch")
	}
	if !earlyRead {
		select {
		case err := <-readDone:
			if err != nil && connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("public read crossing verified finish: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("public read remained blocked after verified finish")
		}
	}
	if _, err := svc.GetVertex(context.Background(), &pb.GetVertexRequest{Key: "kept"}); err != nil {
		t.Fatalf("read after verified finish: %v", err)
	}
}

func TestLanternService_ExclusiveCommittedViewBlocksReceiptLookup(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	viewDone := make(chan error, 1)
	go func() {
		viewDone <- f.service.withExclusiveCommittedView(func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	waitReceiptTest(t, "exclusive view", entered)
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	lookupDone := make(chan error, 1)
	go func() {
		status, _, err := f.coordinator.Lookup(call.Items[0].ID, time.Now().Add(time.Minute))
		if err == nil && status != mutationreceipt.NotYetObserved {
			err = errors.New("Lookup returned unexpected status")
		}
		lookupDone <- err
	}()
	select {
	case err := <-lookupDone:
		t.Fatalf("Lookup crossed exclusive cut: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := waitReceiptTest(t, "exclusive view exit", viewDone); err != nil {
		t.Fatal(err)
	}
	if err := waitReceiptTest(t, "receipt Lookup", lookupDone); err != nil {
		t.Fatal(err)
	}
}
