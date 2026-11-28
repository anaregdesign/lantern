package dataruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type runtimeSearchDocument string

func (d runtimeSearchDocument) String() string                  { return string(d) }
func (d runtimeSearchDocument) PreparationSizeHint() (int, int) { return len(d), 1 }

func durableRuntimeTestConfig(path string) ReceiptDatabaseConfig {
	now := time.Now()
	return ReceiptDatabaseConfig{
		Path: path, BaselineCodec: &receiptBaselineTestCodec{},
		Receipt: mutationreceipt.Config{
			Epoch:          mutationreceipt.Epoch{0x42},
			Retention:      time.Hour,
			MaxEntries:     32,
			MaxBytes:       1 << 20,
			ClockHighWater: now,
		},
		Log:        mutationlog.Options{Capacity: 16, SubscriberBuffer: 2},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			graph.EnablePrefixIndex(func(key string) string { return key })
			graph.EnableSearchIndex(
				func(key string, _ *pb.Vertex) search.Document { return runtimeSearchDocument(key) },
				strings.Compare,
			)
			return nil
		},
		NodeID: hlc.NodeID{0x31},
		Now:    now,
	}
}

type runtimeExpiryObserver struct {
	testPublicationObserver
	first  sync.Once
	commit chan struct{}
	resume chan struct{}
}

func (o *runtimeExpiryObserver) OnPublicationGate(category string, wait, body, unlock time.Duration) {
	o.testPublicationObserver.OnPublicationGate(category, wait, body, unlock)
	if category == "gc" {
		o.first.Do(func() {
			close(o.commit)
			<-o.resume
		})
	}
}

func TestServingRuntimeOnlineExpiryReleasesPublicationGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "graph")
		open := func() *ServingRuntime {
			t.Helper()
			graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
			rt, err := OpenGraphDatabase(t.Context(), dir, hlc.NodeID{1}, graph, mutationlog.Options{Capacity: 8}, "")
			if err != nil {
				t.Fatal(err)
			}
			return rt
		}
		rt := open()
		defer rt.Close()
		executor := rt.NewExecutor(nil)
		observer := &runtimeExpiryObserver{commit: make(chan struct{}), resume: make(chan struct{})}
		executor.WithHotPathMetrics(observer)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		deadline := time.Now().Add(time.Minute)

		const count = 260
		vertices := make([]*pb.Vertex, count)
		for i := range vertices {
			vertices[i] = &pb.Vertex{
				Key:        fmt.Sprintf("expires-%03d", i),
				Expiration: timestamppb.New(deadline),
			}
		}
		if _, err := executor.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: vertices}); err != nil {
			t.Fatal(err)
		}

		finished := make(chan struct{}, 1)
		rt.graph.SetGCHooks(nil, func(time.Duration) {
			select {
			case finished <- struct{}{}:
			default:
			}
		})
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			rt.graph.Watch(ctx, time.Minute)
		}()
		resume := sync.OnceFunc(func() { close(observer.resume) })
		defer func() {
			cancel()
			resume()
			<-watchDone
		}()
		select {
		case <-observer.commit:
		case <-time.After(2 * time.Minute):
			t.Fatal("no durable expiry publication")
		}

		// The observer pauses the watcher after unlocking its first commit.
		// One row has expired physically and a later row is still available
		// for renewal by an independently admitted foreground write.
		rows, err := diskRows()
		if err != nil {
			t.Fatal(err)
		}
		renewed := vertices[count-1].Key
		checkRows := func(store *graphstore.DB, wantRenewed bool) {
			t.Helper()
			err := store.WithCommittedSnapshot(func(generation uint64, snap *tarp.Snapshot) error {
				if _, found, err := rows.GetVertexMetadata(snap, generation, vertices[0].Key); err != nil || found {
					return fmt.Errorf("first expired row found=%t: %v", found, err)
				}
				v, found, err := rows.GetVertex(snap, generation, renewed)
				if err != nil || !found {
					return fmt.Errorf("later row found=%t: %v", found, err)
				}
				if (v.Value.GetString_() == "renewed") != wantRenewed {
					return fmt.Errorf("renewed row=%v, want renewed=%t", v.Value, wantRenewed)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		checkRows(rt.GraphStore(), false)
		if _, err := executor.PutVertex(ctx, &pb.PutVertexRequest{
			Vertex: &pb.Vertex{
				Key:        renewed,
				Value:      &pb.Vertex_String_{String_: "renewed"},
				Expiration: timestamppb.New(time.Now().Add(time.Hour)),
			},
		}); err != nil {
			t.Fatal(err)
		}
		resume()
		select {
		case <-finished:
		case <-time.After(time.Minute):
			t.Fatal("expiry pass did not finish")
		}
		cancel()
		<-watchDone

		checkRows(rt.GraphStore(), true)
		if rt.graph.VertexCount() != 1 {
			t.Fatalf("resident Vertex count = %d, want 1", rt.graph.VertexCount())
		}
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
		reopened := open()
		defer reopened.Close()
		checkRows(reopened.GraphStore(), true)
	})
}

func TestServingRuntimeCertificationRejectsRetainedOversizedFrame(t *testing.T) {
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	clock := hlc.New(hlc.NodeID{0x71}, hlc.Options{})
	runtime, err := newGraphRuntime(graph, log, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	ts := clock.Now()
	mutation := &pb.Mutation{
		Seq: 1, Origin: ts.NodeID[:], Hlc: hlcToProto(ts),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{
				Key:   "retained",
				Value: &pb.Vertex_Bytes{Bytes: make([]byte, 1024)},
			}},
		}},
	}
	if _, err := log.Append(mutation, ts); err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil)
	replication, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}

	err = runtime.CertifyInstallationWithReplicationSendLimit(primary, replication, 128)
	if err == nil || !strings.Contains(err.Error(), "retained replication frame") ||
		!strings.Contains(err.Error(), "LANTERN_MAX_SEND_MSG_BYTES=128") {
		t.Fatalf("small-cap certification = %v", err)
	}
	if runtime.replicationFrameCertified || primary.replicationFrameCertified ||
		replication.replicationFrameCertified {
		t.Fatal("failed certification installed a replication frame limit")
	}
	if err := runtime.CertifyInstallationWithReplicationSendLimit(primary, replication, 2048); err != nil {
		t.Fatalf("larger-cap certification: %v", err)
	}
	if !runtime.replicationFrameCertified ||
		runtime.replicationSendMaxBytes != 2048 ||
		primary.replicationSendMaxBytes != 2048 ||
		replication.replicationSendMaxBytes != 2048 {
		t.Fatal("successful certification did not bind one send limit")
	}
}

func TestServingRuntimeRestartRejectsLoweredSendCap(t *testing.T) {
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "graph.db"))
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallationWithReplicationSendLimit(primary, replication, 0); err != nil {
		t.Fatal(err)
	}
	_, err = primary.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{
			Key:        "retained-restart-frame",
			Value:      &pb.Vertex_Bytes{Bytes: make([]byte, 2048)},
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		}},
	})
	if err != nil {
		t.Fatalf("seed retained frame: %v", err)
	}
	entries := mustRetainedPayloads(t, runtime.log)
	if len(entries) != 1 {
		t.Fatalf("retained entries = %d, want 1", len(entries))
	}
	frameSize, err := validateReplicationFrameSize(entries[0].Op, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary := restarted.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	restartedReplication, err := restarted.NewReplication(restartedPrimary)
	if err != nil {
		t.Fatal(err)
	}
	err = restarted.CertifyInstallationWithReplicationSendLimit(
		restartedPrimary,
		restartedReplication,
		frameSize-1,
	)
	if err == nil || !strings.Contains(err.Error(), "retained replication frame") ||
		!strings.Contains(err.Error(), fmt.Sprintf("LANTERN_MAX_SEND_MSG_BYTES=%d", frameSize-1)) {
		t.Fatalf("lowered-cap restart certification = %v", err)
	}
	if restarted.replicationFrameCertified || restartedPrimary.replicationFrameCertified ||
		restartedReplication.replicationFrameCertified {
		t.Fatal("failed lowered-cap restart installed frame certification")
	}
	if length, _, _ := restarted.MutationLogStats(); length != 1 {
		t.Fatalf("failed lowered-cap restart changed retained log length to %d", length)
	}
	if seq := restartedPrimary.LocalSeq(config.NodeID); seq != 1 {
		t.Fatalf("failed lowered-cap restart changed origin seq to %d", seq)
	}
	if err := restarted.CertifyInstallationWithReplicationSendLimit(
		restartedPrimary,
		restartedReplication,
		frameSize,
	); err != nil {
		t.Fatalf("exact-fit restart certification: %v", err)
	}
}

func TestServingRuntimeRestartRejectsRetainedSparseReceiptRelay(t *testing.T) {
	originConfig := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "origin.wal"))
	originConfig.NodeID = hlc.NodeID{0x41}
	originRuntime, err := OpenReceiptDatabase(t.Context(), originConfig, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = originRuntime.Close() })
	originPrimary := originRuntime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	originReplication, err := originRuntime.NewReplication(originPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if err := originRuntime.CertifyInstallationWithReplicationSendLimit(originPrimary, originReplication, 0); err != nil {
		t.Fatal(err)
	}
	keys := []graphcache.EdgeKey[string]{
		{Tail: "causally-rejected", Head: "receipt-relay"},
		{Tail: "accepted", Head: "receipt-relay"},
	}
	originRuntime.graph.AddEdgeWithExpiration(keys[1].Tail, keys[1].Head, 1, time.Now().Add(time.Hour))
	call := receiptDeleteCall(t, originConfig.Receipt.Epoch, keys...)
	if _, err := originPrimary.receiptEdgeDeleteCoordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	originEnvelope := mustRetainedPayloads(t, originRuntime.log)[0].Op.(*edgeDeleteReceiptEnvelope)
	wire, err := originEnvelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}

	config := originConfig
	config.Path = filepath.Join(t.TempDir(), "follower.db")
	config.NodeID = hlc.NodeID{0x42}
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallationWithReplicationSendLimit(primary, replication, 0); err != nil {
		t.Fatal(err)
	}
	_, err = primary.PutEdges(context.Background(), &pb.PutEdgesRequest{Edges: []*pb.Edge{{
		Tail: keys[0].Tail, Head: keys[0].Head, Weight: 1,
		Expiration: timestamppb.New(time.Now().Add(-time.Second)),
	}}})
	if err != nil {
		t.Fatalf("persist follower's newer causal barrier: %v", err)
	}
	if err := primary.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatalf("apply sparse receiver-local receipt: %v", err)
	}
	if entries := mustRetainedPayloads(t, runtime.log); len(entries) != 2 {
		t.Fatalf("follower retained %d entries, want barrier and receipt", len(entries))
	}
	envelope := mustRetainedPayloads(t, runtime.log)[1].Op.(*edgeDeleteReceiptEnvelope)
	if len(envelope.Accepted) != 1 || envelope.Accepted[0].Index != 1 {
		t.Fatalf("receiver-local receipt accepted indexes = %+v, want only index 1", envelope.Accepted)
	}
	sparseSize, err := validateReplicationFrameSize(envelope, 0)
	if err != nil {
		t.Fatal(err)
	}
	maximalSize, err := validateReplicationFrameSize(maximalReceiptEdgeDeleteEnvelope(envelope), 0)
	if err != nil || maximalSize <= sparseSize {
		t.Fatalf("retained sparse/maximal receipt frame sizes = %d/%d, %v", sparseSize, maximalSize, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary := restarted.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	restartedReplication, err := restarted.NewReplication(restartedPrimary)
	if err != nil {
		t.Fatal(err)
	}
	err = restarted.CertifyInstallationWithReplicationSendLimit(
		restartedPrimary, restartedReplication, maximalSize-1,
	)
	if err == nil || !strings.Contains(err.Error(), "retained replication frame at local log seq 2") ||
		!strings.Contains(err.Error(), fmt.Sprintf("LANTERN_MAX_SEND_MSG_BYTES=%d", maximalSize-1)) {
		t.Fatalf("lowered receiver-local relay cap certification = %v", err)
	}
	if restarted.replicationFrameCertified || restartedPrimary.replicationFrameCertified ||
		restartedReplication.replicationFrameCertified {
		t.Fatal("failed retained receipt certification installed frame limit")
	}
	if err := restarted.CertifyInstallationWithReplicationSendLimit(
		restartedPrimary, restartedReplication, maximalSize,
	); err != nil {
		t.Fatalf("exact-fit retained receipt relay certification: %v", err)
	}
}

func TestServingRuntimeRestartRejectsRetainedSparseReceiptAddRelay(t *testing.T) {
	source := newReceiptEdgeDeleteFixtureWithLimits(
		t, nil, hlc.NodeID{0x51}, 32, 1<<20,
	)
	sourceAdd, err := newEdgeAddReceiptCoordinator(
		source.service,
		source.coordinator.store,
	)
	if err != nil {
		t.Fatal(err)
	}
	call := receiptEdgeAddTestCall(
		t,
		source.epoch,
		0x51,
		&pb.Edge{Tail: "protected-add", Head: "restart", Weight: 1},
		&pb.Edge{Tail: "accepted-add", Head: "restart", Weight: 1},
	)
	if _, err := sourceAdd.Commit(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	wire, err := mustRetainedPayloads(t, source.log)[0].Op.(*graphAddEffectEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}

	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "add-follower.db"))
	config.NodeID = hlc.NodeID{0x52}
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallationWithReplicationSendLimit(
		primary,
		replication,
		0,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.PutEdges(t.Context(), &pb.PutEdgesRequest{Edges: []*pb.Edge{{
		Tail: "protected-add", Head: "restart", Weight: 1,
		Expiration: timestamppb.New(time.Now().Add(-time.Second)),
	}}}); err != nil {
		t.Fatalf("persist follower's newer Add barrier: %v", err)
	}
	if err := primary.ApplyMutation(t.Context(), wire); err != nil {
		t.Fatalf("apply sparse receiver-local Add receipt: %v", err)
	}
	entries := mustRetainedPayloads(t, runtime.log)
	if len(entries) != 2 {
		t.Fatalf("follower retained %d entries, want barrier and Add receipt", len(entries))
	}
	envelope := entries[1].Op.(*graphAddEffectEnvelope)
	if len(envelope.AcceptedIndexes) != 1 || envelope.AcceptedIndexes[0] != 1 {
		t.Fatalf("receiver-local Add accepted indexes = %v, want only index 1", envelope.AcceptedIndexes)
	}
	sparseSize, err := validateReplicationFrameSize(envelope, 0)
	if err != nil {
		t.Fatal(err)
	}
	maximal, err := maximalReplicationRelayEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	maximalSize, err := validateReplicationFrameSize(maximal, 0)
	if err != nil || maximalSize <= sparseSize {
		t.Fatalf("retained sparse/maximal Add frame sizes = %d/%d, %v",
			sparseSize, maximalSize, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary := restarted.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	restartedReplication, err := restarted.NewReplication(restartedPrimary)
	if err != nil {
		t.Fatal(err)
	}
	err = restarted.CertifyInstallationWithReplicationSendLimit(
		restartedPrimary,
		restartedReplication,
		maximalSize-1,
	)
	if err == nil || !strings.Contains(err.Error(), "retained replication frame at local log seq 2") ||
		!strings.Contains(err.Error(), fmt.Sprintf("LANTERN_MAX_SEND_MSG_BYTES=%d", maximalSize-1)) {
		t.Fatalf("lowered receiver-local Add relay cap certification = %v", err)
	}
	if restarted.replicationFrameCertified || restartedPrimary.replicationFrameCertified ||
		restartedReplication.replicationFrameCertified {
		t.Fatal("failed retained Add certification installed frame limit")
	}
	if length, _, _ := restarted.MutationLogStats(); length != 2 {
		t.Fatalf("failed Add certification changed retained log length to %d", length)
	}
	if err := restarted.CertifyInstallationWithReplicationSendLimit(
		restartedPrimary,
		restartedReplication,
		maximalSize,
	); err != nil {
		t.Fatalf("exact-fit retained Add relay certification: %v", err)
	}
}

func TestServingRuntimeGraphOnlyPreservesComposition(t *testing.T) {
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	clock := hlc.New(hlc.NodeID{0x21}, hlc.Options{})
	runtime, err := newGraphRuntime(graph, log, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if replication, err := runtime.NewReplication(nil); replication != nil || err == nil {
		t.Fatalf("nil primary service produced replication service %p, %v", replication, err)
	}
	replication, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	receiptLatched, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	receiptLatched.WithReceiptSnapshotRequired()
	if err := runtime.CertifyInstallation(primary, receiptLatched); err == nil {
		t.Fatal("graph-only runtime certified receipt Snapshot state")
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatalf("graph-only installation certification: %v", err)
	}
	if err := runtime.CertifyInstallation(NewExecutor(graph), replication); err == nil {
		t.Fatal("runtime certified a primary service that bypassed the runtime")
	}
	if runtime.ReceiptsEnabled() {
		t.Fatal("graph-only runtime reported durable receipt WAL")
	}
	if runtime.GraphCache() != graph || runtime.log != log || runtime.clock != clock {
		t.Fatal("graph-only runtime replaced an existing state instance")
	}
	if primary.cache != graph || primary.log != log || primary.clock != clock ||
		primary.runtime != runtime || primary.receiptStore != nil ||
		primary.receiptRetiredCatalog != nil ||
		primary.receiptEdgeDeleteCoordinator != nil {
		t.Fatal("primary service did not receive the exact graph-only bundle")
	}
	if replication.backend != graph || replication.log != log || replication.clock != clock ||
		replication.runtime != runtime || replication.origins != primary ||
		replication.receiptSnapshotRequired || replication.receiptSnapshotSource != nil {
		t.Fatal("replication service did not receive the exact graph-only bundle")
	}
	status, err := replication.PeerStatus(context.Background(), &pb.PeerStatusRequest{})
	if err != nil || status.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 {
		t.Fatalf("graph-only runtime PeerStatus = (%v, %v)", status, err)
	}
	capability, err := primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if capability.GetEnabled() {
		t.Fatal("graph-only runtime enabled receipt capability")
	}

	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close = %v, want idempotent success", err)
	}
	if _, err := log.Append(&pb.Mutation{}, hlc.Timestamp{}); !errors.Is(err, mutationlog.ErrClosed) {
		t.Fatalf("append after Close = %v, want ErrClosed", err)
	}
}

func TestServingRuntimePublicReceiptActivationProofs(t *testing.T) {
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "graph.db"))
	install := func(t *testing.T, runtime *ServingRuntime) (*Executor, *Replication) {
		t.Helper()
		primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
		replication, err := runtime.NewReplication(primary)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.CertifyInstallation(primary, replication); err != nil {
			t.Fatal(err)
		}
		return primary, replication
	}

	fresh, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	primary, replication := install(t, fresh)
	if err := fresh.ActivatePublicReceipts(primary, replication); err == nil {
		t.Fatal("public receipts activated before backup certification")
	}
	if err := fresh.CertifyReceiptBackup(primary, replication); err != nil {
		t.Fatal(err)
	}
	if err := fresh.ActivatePublicReceipts(primary, replication); err != nil {
		t.Fatal(err)
	}
	capability, err := primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || !capability.GetEnabled() ||
		!bytes.Equal(capability.GetPolicy().GetDeploymentEpoch(), config.Receipt.Epoch[:]) ||
		!bytes.Equal(capability.GetEndpoint().GetNodeId(), config.NodeID[:]) ||
		len(capability.GetEndpoint().GetGeneration()) != 16 {
		t.Fatalf("activated capability = %+v, %v", capability, err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	capability, err = primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || capability.GetEnabled() || capability.GetPolicy() != nil ||
		capability.GetEndpoint() != nil {
		t.Fatalf("closed capability = %+v, %v", capability, err)
	}

	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	restartedPrimary, restartedReplication := install(t, restarted)
	if err := restarted.CertifyReceiptBackup(restartedPrimary, restartedReplication); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ActivatePublicReceipts(restartedPrimary, restartedReplication); err != nil {
		t.Fatalf("restart with canonical recovered receipt state: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
}

func TestServingRuntimeDurableFreshRestartCertifiesOneCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	config := durableRuntimeTestConfig(path)
	// Model a wall clock that was previously ahead. Certification must seed
	// the serving HLC above this persisted Store clock even after rollback.
	config.Receipt.ClockHighWater = time.Now().Add(2 * time.Hour)

	fresh, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.ReceiptsEnabled() || fresh.receipt == nil ||
		fresh.receipt.epoch != config.Receipt.Epoch ||
		fresh.receipt.generation == ([16]byte{}) {
		t.Fatalf("fresh runtime receipt identity = %+v", fresh.receipt)
	}
	freshHighWater := fresh.receipt.store.Stats().HighWaterMillis
	freshRetired, _, err := fresh.receipt.retired.snapshot(fresh.receipt.policy, freshHighWater)
	if err != nil || freshRetired.ClockHighWaterMillis != freshHighWater ||
		len(freshRetired.Epochs) != 0 {
		t.Fatalf("fresh retired catalog = %+v, %v", freshRetired, err)
	}
	wantSnapshotPolicy := config.Receipt
	wantSnapshotPolicy.ClockHighWater = time.Time{}
	if fresh.receipt.policy != wantSnapshotPolicy {
		t.Fatalf("fresh runtime receipt Snapshot policy = %+v, want %+v", fresh.receipt.policy, wantSnapshotPolicy)
	}
	generation := fresh.receipt.generation
	primary := fresh.NewExecutor(nil)
	replication, err := fresh.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.CertifyInstallation(primary, replication); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("durable installation without tombstone policy = %v, want failed precondition", err)
	}
	if primary.receiptEdgeDeleteCoordinator != nil ||
		primary.receiptEdgeAddCoordinator != nil ||
		primary.receiptVertexPutCoordinator != nil ||
		primary.receiptVertexDeleteCoordinator != nil {
		t.Fatal("failed certification bound a receipt follower coordinator")
	}
	if replication.receiptSnapshotRequired || replication.receiptSnapshotSource != nil {
		t.Fatal("failed certification partially activated receipt Snapshot")
	}
	primary.WithTombstoneTTL(2 * time.Hour)
	if err := fresh.CertifyInstallation(primary, replication); err != nil {
		t.Fatalf("durable installation certification: %v", err)
	}
	if primary.cache != fresh.graph || primary.log != fresh.log || primary.clock != fresh.clock ||
		primary.origins != fresh.origins || primary.receiptStore != fresh.receipt.store ||
		primary.receiptRetiredCatalog != fresh.receipt.retired ||
		primary.runtime != fresh || primary.receiptEdgeDeleteCoordinator == nil ||
		primary.receiptEdgeDeleteCoordinator.store != fresh.receipt.store ||
		primary.receiptEdgeDeleteCoordinator.retired != fresh.receipt.retired ||
		primary.receiptEdgeAddCoordinator == nil ||
		primary.receiptEdgeAddCoordinator.store != fresh.receipt.store ||
		primary.receiptVertexPutCoordinator == nil ||
		primary.receiptVertexPutCoordinator.store != fresh.receipt.store ||
		primary.receiptVertexDeleteCoordinator == nil ||
		primary.receiptVertexDeleteCoordinator.store != fresh.receipt.store {
		t.Fatal("primary service did not receive the certified durable bundle")
	}
	if replication.backend != fresh.graph || replication.log != fresh.log ||
		replication.clock != fresh.clock || replication.origins != primary ||
		replication.runtime != fresh || !replication.receiptSnapshotRequired ||
		replication.receiptSnapshotSource == nil ||
		replication.receiptSnapshotSource.owner != primary ||
		replication.receiptSnapshotSource.store != fresh.receipt.store ||
		replication.receiptSnapshotSource.retired != fresh.receipt.retired ||
		replication.receiptSnapshotPolicy != wantSnapshotPolicy {
		t.Fatal("replication service did not receive the certified durable bundle")
	}
	status, err := replication.PeerStatus(context.Background(), &pb.PeerStatusRequest{})
	if err != nil || status.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
		t.Fatalf("durable runtime PeerStatus = (%v, %v)", status, err)
	}
	recorder := &replicationSnapshotRecorder{}
	if err := replication.Snapshot(context.Background(), &pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
	}, recorder); err != nil {
		t.Fatalf("durable runtime receipt Snapshot: %v", err)
	}
	if err := validateReceiptSnapshotFrames(
		recorder.frames,
		wantSnapshotPolicy,
		mutationreceipt.RetiredCatalogConfig{
			ActiveEpoch: wantSnapshotPolicy.Epoch,
			MaxEntries:  wantSnapshotPolicy.MaxEntries,
			MaxBytes:    wantSnapshotPolicy.MaxBytes,
		},
	); err != nil {
		t.Fatalf("durable runtime receipt Snapshot frames: %v", err)
	}
	configuredSource := replication.receiptSnapshotSource
	if err := fresh.CertifyInstallation(primary, replication); err != nil {
		t.Fatalf("repeat durable installation certification: %v", err)
	}
	if replication.receiptSnapshotSource != configuredSource {
		t.Fatal("repeat durable installation certification replaced the receipt Snapshot source")
	}
	capability, err := primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if capability.GetEnabled() {
		t.Fatal("durable runtime enabled public receipt capability")
	}
	if _, err := primary.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: validReceiptOperationIDForTest(t, 0x02),
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("durable runtime receipt status error = %v, want failed precondition", err)
	}
	wantClockFloor := config.Receipt.ClockHighWater.UnixMilli() * int64(time.Millisecond)
	if next := fresh.clock.Now(); !(hlc.Timestamp{WallNs: wantClockFloor}).Less(next) {
		t.Fatalf("restored HLC = %+v, want above persisted high-water %d", next, wantClockFloor)
	}

	wall := time.Now().UnixNano()
	entry := recoveryGraphPutEffectEntry(t, 0x53, wall, &pb.MutationOp{
		Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{
				Key:        "restored-item",
				Expiration: timestamppb.New(time.Now().Add(time.Hour)),
			},
		}},
	})
	frame, err := subscribeMutationFrame(entry.Op)
	if err != nil {
		t.Fatal(err)
	}
	if err = primary.ApplyMutation(t.Context(), frame.GetMutation()); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if restarted.receipt.generation != generation {
		t.Fatalf("generation changed across same-epoch restart: %x != %x", restarted.receipt.generation, generation)
	}
	restartedHighWater := restarted.receipt.store.Stats().HighWaterMillis
	restartedRetired, _, err := restarted.receipt.retired.snapshot(
		restarted.receipt.policy,
		restartedHighWater,
	)
	if err != nil || restartedRetired.ClockHighWaterMillis != restartedHighWater ||
		len(restartedRetired.Epochs) != 0 {
		t.Fatalf("marker-free restart retired catalog = %+v, %v", restartedRetired, err)
	}
	if vertex, ok := restarted.graph.GetVertex("restored-item"); !ok || vertex.GetKey() != "restored-item" {
		t.Fatalf("durable mutation recovery = %v, %v", vertex, ok)
	}
	if got := restarted.graph.CountByPrefix("restored-"); got != 1 {
		t.Fatalf("rebuilt prefix index count = %d, want 1", got)
	}
	if hits := restarted.graph.SearchVertices("restored", 10, ""); len(hits) != 1 || hits[0].ID != "restored-item" {
		t.Fatalf("rebuilt search index = %+v, want restored-item", hits)
	}
	if next := restarted.clock.Now(); !entry.HLC.Less(next) {
		t.Fatalf("restarted HLC = %+v, want above WAL frontier %+v", next, entry.HLC)
	}
	states := restarted.origins.States()
	if len(states) != 1 || states[0].LastSeq != 1 || states[0].LastHLC != entry.HLC {
		t.Fatalf("restarted origin cut = %+v", states)
	}
}

func TestServingRuntimeDurablePersistsGenericRemoteClockFloor(t *testing.T) {
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "graph.db"))
	config.Receipt.ClockHighWater = time.Now().Add(10 * time.Second).Truncate(time.Millisecond)
	origin := hlc.NodeID{0x62}
	firstStamp := hlc.Timestamp{
		WallNs:  config.Receipt.ClockHighWater.Add(hlc.DefaultMaxSkew / 2).UnixNano(),
		Logical: 3,
		NodeID:  origin,
	}
	mutation := func(seq uint64, stamp hlc.Timestamp, op *pb.MutationOp) *pb.Mutation {
		return &pb.Mutation{
			Seq: seq, Origin: origin[:], Hlc: hlcToProto(stamp), Op: op,
		}
	}

	fresh, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	freshClosed := false
	t.Cleanup(func() {
		if !freshClosed {
			_ = fresh.Close()
		}
	})
	primary := fresh.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replication, err := fresh.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	expiration := timestamppb.New(time.Unix(0, firstStamp.WallNs).Add(time.Hour))
	if err := primary.ApplyMutation(context.Background(), mutation(1, firstStamp, &pb.MutationOp{
		Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "before-restart", Expiration: expiration},
		}},
	})); err != nil {
		t.Fatalf("first generic remote mutation: %v", err)
	}
	if highWater := fresh.receipt.store.Stats().HighWaterMillis; highWater < firstStamp.WallNs/int64(time.Millisecond) {
		t.Fatalf("fresh Store high-water = %d, want at least %d", highWater, firstStamp.WallNs/int64(time.Millisecond))
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	freshClosed = true

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary := restarted.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	restartedReplication, err := restarted.NewReplication(restartedPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.CertifyInstallation(restartedPrimary, restartedReplication); err != nil {
		t.Fatal(err)
	}
	if highWater := restarted.receipt.store.Stats().HighWaterMillis; highWater < firstStamp.WallNs/int64(time.Millisecond) {
		t.Fatalf("restarted Store high-water = %d, want at least %d", highWater, firstStamp.WallNs/int64(time.Millisecond))
	}

	secondStamp := firstStamp
	secondStamp.WallNs += int64(hlc.DefaultMaxSkew / 2)
	secondStamp.Logical++
	if err := restartedPrimary.ApplyMutation(context.Background(), mutation(2, secondStamp, &pb.MutationOp{
		Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "after", Head: "restart", Weight: 1, Expiration: expiration},
		}},
	})); err != nil {
		t.Fatalf("second generic remote mutation after restart: %v", err)
	}
	source, err := NewReceiptWholeStateSource(restartedPrimary, restarted.receipt.store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.Capture(context.Background(), config.Receipt)
	if err != nil {
		t.Fatalf("receipt capture after restarted generic publication: %v", err)
	}
	if len(capture.Origins) != 1 || capture.Origins[0].LastHLC != secondStamp {
		t.Fatalf("restarted capture origin state = %+v", capture.Origins)
	}
	if len(capture.Graph) == 0 || capture.Graph[0].GetHeader() == nil {
		t.Fatalf("restarted receipt capture has no header: %+v", capture.Graph)
	}
	if cutoff := hlcFromProto(capture.Graph[0].GetHeader().GetCutoffHlc()); !secondStamp.Less(cutoff) {
		t.Fatalf("restarted receipt cutoff %v did not exceed remote origin %v", cutoff, secondStamp)
	}
}

func TestNamespacedRuntimeSelectsEdgeHistoryBeforeWrites(t *testing.T) {
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	if _, err := newGraphRuntime(graph, log, hlc.New(hlc.NodeID{1}, hlc.Options{}), "namespaced-v1"); err != nil {
		t.Fatal(err)
	}
	if !graph.RetainsDanglingEdgeHistory() {
		t.Fatal("private runtime omitted retained history")
	}
	occupied := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	if err := occupied.PutVertex("data:existing", &pb.Vertex{Key: "data:existing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := newGraphRuntime(occupied, log, hlc.New(hlc.NodeID{2}, hlc.Options{}), "namespaced-v1"); err == nil {
		t.Fatal("late history policy selection admitted")
	}
	config := durableRuntimeTestConfig(t.TempDir() + "/history.wal")
	config.NamespaceFormat = "namespaced-v1"
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.GraphCache().RetainsDanglingEdgeHistory() {
		t.Fatal("fresh durable runtime omitted history")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resumed.Close(); err != nil {
			t.Errorf("close resumed runtime: %v", err)
		}
	}()
	if !resumed.GraphCache().RetainsDanglingEdgeHistory() {
		t.Fatal("durable restart omitted history")
	}
}
