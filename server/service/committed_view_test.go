package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func publicationReadWaitBarrier(svc *LanternService, category string) <-chan struct{} {
	waiting := make(chan struct{})
	var once sync.Once
	svc.publicationClock = func() time.Time {
		if svc.publicationWaiting.count(category) != 0 {
			once.Do(func() { close(waiting) })
		}
		return time.Now()
	}
	return waiting
}

func TestPublicGraphCertificateBusyBeforeCaptureUsesCommittedFallback(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(map[bool]string{false: "published", true: "WAL fault"}[fault], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			log := mutationlog.New(mutationlog.Options{Capacity: 8})
			defer closeObservationLog(t, log)
			metrics := &testPublicationObserver{}
			svc := NewLanternService(cache).WithHotPathMetrics(metrics)
			waiting := publicationReadWaitBarrier(svc, "read_fallback")
			svc.replicationCutMu.Lock()
			var once sync.Once
			release := func() { once.Do(svc.replicationCutMu.Unlock) }
			defer release()
			if err := cache.PutVertex("partial", &pb.Vertex{Key: "partial"}); err != nil {
				t.Fatal(err)
			}
			var captures atomic.Int32
			var graphCount, logCount uint64
			done := make(chan error, 1)
			go func() {
				done <- svc.withPublicGraphReadRetry(ctx, func() error {
					captures.Add(1)
					graphCount = uint64(cache.VertexCount())
					logCount, _ = log.LastSeq()
					return nil
				})
			}()
			select {
			case <-waiting:
			case err := <-done:
				t.Fatalf("active writer allowed early read: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if captures.Load() != 0 {
				t.Fatal("busy certificate executed an optimistic capture")
			}
			if fault {
				svc.markPublicationFault(&pendingMutation{})
			} else if _, err := log.Append(&pb.Mutation{}, hlc.Timestamp{}); err != nil {
				t.Fatal(err)
			}
			release()
			select {
			case err := <-done:
				if fault {
					if connect.CodeOf(err) != connect.CodeFailedPrecondition || captures.Load() != 0 {
						t.Fatalf("unpublished read: captures=%d err=%v", captures.Load(), err)
					}
				} else if err != nil || captures.Load() != 1 || graphCount != 1 || logCount != 1 ||
					len(metrics.reads) != 1 || metrics.reads[0].attempt != 4 {
					t.Fatalf("committed fallback: graph=%d log=%d captures=%d err=%v", graphCount, logCount, captures.Load(), err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestPublicGraphCertificateActiveAfterCapturePreservesFaultAndErrorOrder(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(map[bool]string{false: "capture error", true: "fault before capture error"}[fault], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			svc := NewLanternService(newFakeBackend()).WithHotPathMetrics(&testPublicationObserver{})
			waiting := publicationReadWaitBarrier(svc, "committed_view")
			active, writerDone, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			defer finish()
			captureErr := errors.New("original detached capture failed")
			var captures atomic.Int32
			done := make(chan error, 1)
			go func() {
				done <- svc.withPublicGraphReadRetry(ctx, func() error {
					captures.Add(1)
					go func() {
						svc.replicationCutMu.Lock()
						if fault {
							svc.markPublicationFault(&pendingMutation{})
						}
						close(active)
						<-release
						svc.replicationCutMu.Unlock()
						close(writerDone)
					}()
					<-active
					return captureErr
				})
			}()
			select {
			case <-waiting:
			case err := <-done:
				t.Fatalf("capture error escaped the still-active publication cut: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			finish()
			select {
			case err := <-done:
				if captures.Load() != 1 || (fault && connect.CodeOf(err) != connect.CodeFailedPrecondition) ||
					(!fault && !errors.Is(err, captureErr)) {
					t.Fatalf("captures=%d fault=%t err=%v", captures.Load(), fault, err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-writerDone:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestPublicGraphCertificateFaultRepairAndOneShotRemainDistinct(t *testing.T) {
	svc := NewLanternService(newFakeBackend())
	captures := 0
	err := svc.withPublicGraphReadRetry(t.Context(), func() error {
		captures++
		if captures == 1 {
			pending := &pendingMutation{}
			svc.replicationCutMu.Lock()
			svc.markPublicationFault(pending)
			svc.replicationCutMu.Unlock()
			svc.replicationCutMu.Lock()
			svc.clearPublicationFault(pending)
			svc.replicationCutMu.Unlock()
		}
		return nil
	})
	if err != nil || captures != 2 {
		t.Fatalf("fault+repair crossing capture was not invalidated: captures=%d err=%v", captures, err)
	}
	captures = 0
	err = svc.withPublicGraphRead(func() error {
		captures++
		svc.replicationCutMu.Lock()
		active := svc.replicationCutMu.readCertificate.Load()
		svc.replicationCutMu.Unlock()
		if active.stable() {
			t.Fatal("no-effect writer did not publish an active interval")
		}
		return nil
	})
	if captures != 1 || connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("one-shot Search contract changed: captures=%d err=%v", captures, err)
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	finish(false)
	if !svc.replicationCutMu.readCertificate.Load().faulted {
		t.Fatal("failed Snapshot install produced healthy certificate")
	}
	finish, err = svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	finish(true)
	if certificate := svc.replicationCutMu.readCertificate.Load(); !certificate.stable() || certificate.faulted {
		t.Fatal("verified Snapshot did not publish a new healthy certificate")
	}
}

func TestPublicGraphCertificateCancellationKeepsBoundedCallbackOwnership(t *testing.T) {
	for _, phase := range []string{"entry callback", "queued fallback", "acquired fallback"} {
		t.Run(phase, func(t *testing.T) {
			guard, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(guard)
			defer cancel()
			svc := NewLanternService(newFakeBackend()).WithHotPathMetrics(&testPublicationObserver{})
			var captures atomic.Int32
			if phase == "entry callback" {
				// Keep the existing first-capture error contract: cancellation
				// already observed by a callback is returned once, without retry.
				cancel()
				err := svc.withPublicGraphReadRetry(ctx, func() error {
					captures.Add(1)
					return ctxToConnect(ctx.Err())
				})
				if connect.CodeOf(err) != connect.CodeCanceled || captures.Load() != 1 {
					t.Fatalf("entry cancellation: captures=%d err=%v", captures.Load(), err)
				}
				return
			}
			waiting := make(chan struct{})
			var samples atomic.Int32
			svc.publicationClock = func() time.Time {
				if svc.publicationWaiting.count("read_fallback") != 0 {
					switch samples.Add(1) {
					case 1:
						close(waiting)
					case 2:
						if phase == "acquired fallback" {
							cancel()
						}
					}
				}
				return time.Now()
			}
			svc.replicationCutMu.Lock()
			var once sync.Once
			release := func() { once.Do(svc.replicationCutMu.Unlock) }
			defer release()
			done := make(chan error, 1)
			go func() {
				done <- svc.withPublicGraphReadRetry(ctx, func() error { captures.Add(1); return nil })
			}()
			select {
			case <-waiting:
			case <-guard.Done():
				t.Fatal(guard.Err())
			}
			if phase == "queued fallback" {
				cancel()
			}
			release()
			select {
			case err := <-done:
				if connect.CodeOf(err) != connect.CodeCanceled || captures.Load() != 0 || samples.Load() != 2 {
					t.Fatalf("fallback cancellation: samples=%d captures=%d err=%v", samples.Load(), captures.Load(), err)
				}
			case <-guard.Done():
				t.Fatal(guard.Err())
			}
		})
	}
}

func TestPublicGraphCertificateActiveAfterSuccessfulCaptureDiscardsPartialCut(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	defer closeObservationLog(t, log)
	svc := NewLanternService(cache).WithHotPathMetrics(&testPublicationObserver{})
	waiting := publicationReadWaitBarrier(svc, "read_fallback")
	active, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	writerDone := make(chan error, 1)
	var captures atomic.Int32
	var graphCount, logCount uint64
	done := make(chan error, 1)
	go func() {
		done <- svc.withPublicGraphReadRetry(ctx, func() error {
			attempt := captures.Add(1)
			graphCount = uint64(cache.VertexCount())
			logCount, _ = log.LastSeq()
			if attempt == 1 {
				go func() {
					svc.replicationCutMu.Lock()
					err := cache.PutVertex("committed", &pb.Vertex{Key: "committed"})
					close(active)
					<-release
					if err == nil {
						_, err = log.Append(&pb.Mutation{}, hlc.Timestamp{})
					}
					svc.replicationCutMu.Unlock()
					writerDone <- err
				}()
				<-active
			}
			return nil
		})
	}()
	select {
	case <-waiting:
	case err := <-done:
		t.Fatalf("overlapped data escaped before publication: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if captures.Load() != 1 || graphCount != 0 || logCount != 0 {
		t.Fatal("partial cut was reused or a second capture escaped the active writer")
	}
	finish()
	select {
	case err := <-done:
		if err != nil || captures.Load() != 2 || graphCount != 1 || logCount != 1 {
			t.Fatalf("replacement committed result: captures=%d graph=%d log=%d err=%v", captures.Load(), graphCount, logCount, err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

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

func TestLanternService_PublicGraphReadRetrySerializesAndFailsClosed(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewLanternService(newFakeBackend()).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	ctx := context.Background()
	attempts := 0
	if err := svc.withPublicGraphReadRetry(ctx, func() error {
		attempts++
		if attempts == 1 {
			svc.replicationCutMu.Lock()
			svc.replicationCutMu.Unlock()
		}
		return nil
	}); err != nil || attempts != 2 {
		t.Fatalf("one overlapping publication = %v, attempts=%d; want stable retry", err, attempts)
	}

	attempts = 0
	err := svc.withPublicGraphReadRetry(ctx, func() error {
		attempts++
		if attempts <= 3 {
			svc.replicationCutMu.Lock()
			svc.replicationCutMu.Unlock()
		}
		return nil
	})
	if err != nil || attempts != 4 {
		t.Fatalf("continuous publications = %v, attempts=%d; want one serialized capture", err, attempts)
	}
	captureErr := errors.New("capture failed")
	attempts = 0
	err = svc.withPublicGraphReadRetry(ctx, func() error {
		attempts++
		svc.replicationCutMu.Lock()
		svc.replicationCutMu.Unlock()
		return captureErr
	})
	if !errors.Is(err, captureErr) || attempts != 1 {
		t.Fatalf("failed capture = %v, attempts=%d; want original failure without retry", err, attempts)
	}
	cancelledCtx, cancel := context.WithCancel(ctx)
	attempts = 0
	err = svc.withPublicGraphReadRetry(cancelledCtx, func() error {
		attempts++
		svc.replicationCutMu.Lock()
		svc.replicationCutMu.Unlock()
		cancel()
		return nil
	})
	if connect.CodeOf(err) != connect.CodeCanceled || attempts != 1 {
		t.Fatalf("cancelled retry = %v, attempts=%d; want immediate Canceled", err, attempts)
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	defer finish(false)
	attempts = 0
	err = svc.withPublicGraphReadRetry(ctx, func() error { attempts++; return nil })
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || attempts != 0 {
		t.Fatalf("faulted retry = %v, attempts=%d; want fail-closed without capture", err, attempts)
	}
}

func TestLanternService_PublicGraphReadFallbackBlocksPublication(t *testing.T) {
	svc := NewLanternService(newFakeBackend())
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	attempts := 0
	readDone := make(chan error, 1)
	go func() {
		readDone <- svc.withPublicGraphReadRetry(context.Background(), func() error {
			attempts++
			if attempts <= 3 {
				svc.replicationCutMu.Lock()
				svc.replicationCutMu.Unlock()
			} else {
				close(entered)
				<-release
			}
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("serialized capture did not start")
	}
	writerDone := make(chan struct{})
	go func() {
		svc.replicationCutMu.Lock()
		svc.replicationCutMu.Unlock()
		close(writerDone)
	}()
	select {
	case <-writerDone:
		t.Fatal("publication entered during serialized graph read")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-readDone:
		if err != nil || attempts != 4 {
			t.Fatalf("serialized graph read = %v, attempts=%d", err, attempts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serialized graph read did not finish")
	}
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("publication remained blocked after graph read")
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
