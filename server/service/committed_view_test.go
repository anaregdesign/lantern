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
