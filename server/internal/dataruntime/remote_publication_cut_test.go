package dataruntime

import (
	"context"
	"encoding/hex"
	"math"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

// An unrelated reader remains held until Apply returns. Success or validation
// refusal therefore proves the call did not queue for exclusive publication.
func applyReadOnlyDuplicate(t *testing.T, svc *Executor, m *pb.Mutation) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	svc.replicationCutMu.RLock()
	defer svc.replicationCutMu.RUnlock()
	generation, certificate := svc.replicationCutMu.generation, svc.replicationCutMu.readCertificate.Load()
	pending, pendingBytes := svc.pendingCount, svc.pendingBytes
	entries := svc.log.RetainedEntries()
	states := func() any {
		if svc.origins == nil {
			return nil
		}
		return svc.origins.States()
	}
	beforeStates := states()
	var storeBefore any
	if svc.receiptStore != nil {
		storeBefore = svc.receiptStore.Stats()
	}
	done := make(chan error, 1)
	go func() { done <- svc.ApplyMutation(ctx, m) }()
	select {
	case err := <-done:
		if svc.replicationCutMu.generation != generation || svc.replicationCutMu.readCertificate.Load() != certificate ||
			svc.pendingCount != pending || svc.pendingBytes != pendingBytes ||
			!reflect.DeepEqual(entries, svc.log.RetainedEntries()) || !reflect.DeepEqual(beforeStates, states()) {
			t.Fatal("read-only proof changed publication/log/origin/pending state")
		}
		if svc.receiptStore != nil && !reflect.DeepEqual(storeBefore, svc.receiptStore.Stats()) {
			t.Fatal("read-only duplicate changed Store state/high-water")
		}
		return err
	case <-ctx.Done():
		t.Fatal("committed replay queued behind an unrelated RLock", ctx.Err())
		return ctx.Err()
	}
}

func TestRemotePublicationCutDedupsEvictedAndSnapshotCoveredSequences(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 2})
	defer closeObservationLog(t, log)
	var clockSamples atomic.Int64
	wall := time.Now().UnixNano()
	clock := hlc.New(hlc.NodeID{1}, hlc.Options{Now: func() int64 { clockSamples.Add(1); return wall }})
	metrics := &testPublicationObserver{}
	svc := NewExecutor(cache).WithReplication(log, clock, nil).WithHotPathMetrics(metrics)
	origin := hlc.NodeID{2}
	for seq := uint64(1); seq <= 5; seq++ {
		if err := svc.ApplyMutation(t.Context(), observationMutation(origin, seq)); err != nil {
			t.Fatal(err)
		}
	}
	beforeClock := clockSamples.Load()
	for _, seq := range []uint64{1, 3, 5} {
		m := observationMutation(origin, seq)
		m.GetOp().GetPutVertex().GetVertex().Key = "must-not-publish"
		if err := applyReadOnlyDuplicate(t, svc, m); err != nil {
			t.Fatal(err)
		}
		if sample := metrics.applies[len(metrics.applies)-1]; sample.acquired || sample.committed != 0 || sample.outcome != "committed_duplicate" {
			t.Fatalf("read-only observation = %+v", sample)
		}
	}
	if clockSamples.Load() != beforeClock {
		t.Fatal("committed duplicate advanced the HLC")
	}
	if _, present := cache.GetVertex("must-not-publish"); present {
		t.Fatal("otherwise-valid changed committed payload was applied")
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplySnapshotWatermarks(map[string]uint64{hex.EncodeToString(origin[:]): 20}, hlc.Timestamp{WallNs: wall, NodeID: origin}); err != nil {
		finish(false)
		t.Fatal(err)
	}
	if err := applyReadOnlyDuplicate(t, svc, observationMutation(origin, 10)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		finish(false)
		t.Fatalf("active Snapshot duplicate = %v", err)
	}
	finish(true)
	if err := applyReadOnlyDuplicate(t, svc, observationMutation(origin, 10)); err != nil || log.Len() != 2 {
		t.Fatalf("verified Snapshot-covered sequence = %v, log=%d", err, log.Len())
	}
}

func TestRemotePublicationCutMalformedCommittedSequencesStillReject(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	defer closeObservationLog(t, log)
	svc := NewExecutor(newFakeBackend()).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	origin := hlc.NodeID{2}
	if err := svc.ApplyMutation(t.Context(), observationMutation(origin, 1)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*pb.Mutation)
		code connect.Code
	}{
		{"missing op", func(m *pb.Mutation) { m.Op = nil }, connect.CodeInvalidArgument},
		{"empty op", func(m *pb.Mutation) { m.Op = &pb.MutationOp{} }, connect.CodeInvalidArgument},
		{"zero seq", func(m *pb.Mutation) { m.Seq = 0 }, connect.CodeInvalidArgument},
		{"short origin", func(m *pb.Mutation) { m.Origin = m.Origin[:15] }, connect.CodeInvalidArgument},
		{"zero origin", func(m *pb.Mutation) { m.Origin = make([]byte, 16) }, connect.CodeInvalidArgument},
		{"missing HLC", func(m *pb.Mutation) { m.Hlc = nil }, connect.CodeInvalidArgument},
		{"short HLC ID", func(m *pb.Mutation) { m.Hlc.NodeId = m.Hlc.NodeId[:15] }, connect.CodeInvalidArgument},
		{"zero HLC ID", func(m *pb.Mutation) { m.Hlc.NodeId = make([]byte, 16) }, connect.CodeInvalidArgument},
		{"mismatched HLC", func(m *pb.Mutation) { m.Hlc.NodeId = append([]byte(nil), m.Hlc.NodeId...); m.Hlc.NodeId[0]++ }, connect.CodeInvalidArgument},
		{"namespace", func(m *pb.Mutation) { m.NamespaceFormat = "unexpected" }, connect.CodeFailedPrecondition},
		{"predicate Delete", func(m *pb.Mutation) {
			m.Op.Op = &pb.MutationOp_DeleteVerticesByPrefix{DeleteVerticesByPrefix: &pb.DeleteVerticesByPrefixRequest{}}
		}, connect.CodeInvalidArgument},
		{"nonfinite Add", func(m *pb.Mutation) {
			m.Op.Op = &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: float32(math.NaN())}}}
		}, connect.CodeInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := proto.Clone(observationMutation(origin, 1)).(*pb.Mutation)
			tt.edit(m)
			if err := applyReadOnlyDuplicate(t, svc, m); connect.CodeOf(err) != tt.code {
				t.Fatalf("malformed committed sequence = %v, want %v", err, tt.code)
			}
		})
	}
	svc.replicationCutMu.Lock()
	svc.markReceiptCommitFaultLocked()
	svc.replicationCutMu.Unlock()
	bad := observationMutation(origin, 1)
	bad.Hlc = nil
	if err := applyReadOnlyDuplicate(t, svc, bad); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("early publication-shape guard lost priority: %v", err)
	}
	svc.replicationCutMu.Lock()
	svc.origins = nil
	svc.replicationCutMu.Unlock()
	if err := applyReadOnlyDuplicate(t, svc, observationMutation(origin, 1)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("receipt fault lost priority over unavailable origin state: %v", err)
	}
}

func TestRemotePublicationCutRevalidatesAfterNonduplicateProbe(t *testing.T) {
	for _, transition := range []string{"committed", "Snapshot fault", "receipt fault", "clock unavailable"} {
		t.Run(transition, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			fixture := newReceiptEdgeDeleteFixture(t, nil)
			svc := fixture.service
			metrics := &testPublicationObserver{}
			svc.WithHotPathMetrics(metrics)
			requested, resume := make(chan struct{}), make(chan struct{})
			var paused, resumed atomic.Bool
			release := func() {
				if resumed.CompareAndSwap(false, true) {
					close(resume)
				}
			}
			defer release()
			svc.publicationClock = func() time.Time {
				if svc.publicationWaiting.count("remote_apply") != 0 && paused.CompareAndSwap(false, true) {
					close(requested)
					<-resume
				}
				return time.Now()
			}
			origin := hlc.NodeID{2}
			m := observationMutation(origin, 1)
			m.Hlc.WallNs = time.Now().UnixNano()
			done := make(chan error, 1)
			go func() { done <- svc.ApplyMutation(ctx, m) }()
			select {
			case <-requested:
			case err := <-done:
				t.Fatalf("valid nonduplicate failed before exclusive reacquisition: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			want := connect.CodeFailedPrecondition
			switch transition {
			case "committed":
				svc.replicationCutMu.Lock()
				err := svc.publishRemoteMutation(ctx, m, nil)
				svc.replicationCutMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			case "Snapshot fault":
				finish, err := svc.BeginSnapshotInstall()
				if err != nil {
					t.Fatal(err)
				}
				defer finish(false)
			case "receipt fault":
				svc.replicationCutMu.Lock()
				svc.markReceiptCommitFaultLocked()
				svc.replicationCutMu.Unlock()
			case "clock unavailable":
				svc.replicationCutMu.Lock()
				svc.clock = nil
				svc.replicationCutMu.Unlock()
				want = connect.CodeInternal
			}
			release()
			select {
			case err := <-done:
				if transition == "committed" {
					if err != nil || fixture.log.Len() != 1 || svc.LocalSeq(origin) != 1 {
						t.Fatalf("exclusive recheck republished: err=%v log=%d", err, fixture.log.Len())
					}
				} else if connect.CodeOf(err) != want || fixture.log.Len() != 0 {
					t.Fatalf("stale read proof used after %s: err=%v log=%d", transition, err, fixture.log.Len())
				}
				if len(metrics.applies) != 1 || !metrics.applies[0].acquired {
					t.Fatal("nonduplicate did not retain exclusive admission")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestRemotePublicationCutTryReadMissUsesOriginalWriterPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	defer closeObservationLog(t, log)
	metrics := &testPublicationObserver{}
	svc := NewExecutor(newFakeBackend()).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil).WithHotPathMetrics(metrics)
	requested := publicationReadWaitBarrier(svc, "remote_apply")
	svc.replicationCutMu.Lock()
	var released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			svc.replicationCutMu.Unlock()
		}
	}
	defer release()
	done := make(chan error, 1)
	go func() { done <- svc.ApplyMutation(ctx, observationMutation(hlc.NodeID{2}, 1)) }()
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal("TryRLock miss did not reach original exclusive acquisition", ctx.Err())
	}
	if log.Len() != 0 || svc.publicationWaiting.count("remote_apply") != 1 {
		t.Fatal("failed read proof published or bypassed the writer cut")
	}
	release()
	select {
	case err := <-done:
		if err != nil || log.Len() != 1 || len(metrics.applies) != 1 || !metrics.applies[0].acquired {
			t.Fatalf("original writer path: err=%v log=%d", err, log.Len())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestRemotePublicationCutReadProofOrdersSnapshotFaultAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	defer closeObservationLog(t, log)
	svc := NewExecutor(newFakeBackend()).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil).
		WithHotPathMetrics(&testPublicationObserver{})
	m := observationMutation(hlc.NodeID{2}, 1)
	if err := svc.ApplyMutation(ctx, m); err != nil {
		t.Fatal(err)
	}
	waiting := publicationReadWaitBarrier(svc, "snapshot")
	if !svc.replicationCutMu.TryRLock() {
		t.Fatal("uncontended proof cut failed")
	}
	held := true
	defer func() {
		if held {
			svc.replicationCutMu.RUnlock()
		}
	}()
	cut, err := svc.checkRemotePublicationAtCut(m, nil)
	if err != nil || cut.committed != 1 {
		t.Fatalf("committed read proof = %+v, %v", cut, err)
	}
	type installation struct {
		finish func(bool)
		err    error
	}
	installed := make(chan installation, 1)
	go func() { finish, err := svc.BeginSnapshotInstall(); installed <- installation{finish, err} }()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if svc.snapshotInstallFaulted || svc.publicationFaultCount != 0 {
		t.Fatal("Snapshot fault crossed the held read-proof cut")
	}
	svc.replicationCutMu.RUnlock()
	held = false
	var begin installation
	select {
	case begin = <-installed:
		if begin.err != nil {
			t.Fatal(begin.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := applyReadOnlyDuplicate(t, svc, m); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		begin.finish(false)
		t.Fatalf("duplicate accepted after Snapshot fault admission: %v", err)
	}
	begin.finish(false)
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	finish(true)
	if err := applyReadOnlyDuplicate(t, svc, m); err != nil {
		t.Fatalf("verified Snapshot did not reopen duplicate proof: %v", err)
	}
}

func TestRemotePublicationCutLegacyWALRetryStaysExclusive(t *testing.T) {
	cache := newFakeBackend()
	wal := &heldPublicationWAL{}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	defer closeObservationLog(t, log)
	svc := NewExecutor(cache).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	origin := hlc.NodeID{2}
	first := observationMutation(origin, 1)
	if err := svc.ApplyMutation(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := observationMutation(origin, 2)
	second.Op.Op = &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 2, Expiration: futureTs(time.Hour)}}}
	wal.failed.Store(true)
	if err := svc.ApplyMutation(t.Context(), second); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("injected WAL fault = %v", err)
	}
	pending := svc.pendingMutations[origin][2]
	if pending == nil || !pending.applied || svc.LocalSeq(origin) != 1 || !svc.replicationCutMu.readCertificate.Load().faulted {
		t.Fatal("graph-applied effect was falsely committed")
	}
	retainedEffect := pending.walOp
	if err := applyReadOnlyDuplicate(t, svc, first); err != nil {
		t.Fatalf("unrelated legacy WAL fault blocked older committed proof: %v", err)
	}
	if _, err := svc.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "observed"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("older duplicate cleared public refusal: %v", err)
	}
	wal.failed.Store(false)
	if err := svc.ApplyMutation(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if svc.LocalSeq(origin) != 2 || log.Len() != 2 || cache.edges["a"]["b"] != 2 ||
		mustMutationLogEntry(t, log, 2).Op != retainedEffect || svc.replicationCutMu.readCertificate.Load().faulted {
		t.Fatal("pending WAL retry changed original effect or applied twice")
	}
}
