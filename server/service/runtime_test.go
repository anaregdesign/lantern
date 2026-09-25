package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type runtimeSearchDocument string

func (d runtimeSearchDocument) String() string { return string(d) }

func durableRuntimeTestConfig(path string) DurableReceiptWALRuntimeConfig {
	now := time.Now()
	return DurableReceiptWALRuntimeConfig{
		Path: path,
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

func TestServingRuntimeGraphOnlyPreservesComposition(t *testing.T) {
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	clock := hlc.New(hlc.NodeID{0x21}, hlc.Options{})
	runtime, err := NewGraphOnlyServingRuntime(graph, log, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	if replication, err := runtime.NewLanternReplicationService(nil); replication != nil || err == nil {
		t.Fatalf("nil primary service produced replication service %p, %v", replication, err)
	}
	replication, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	receiptLatched, err := runtime.NewLanternReplicationService(primary)
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
	if err := runtime.CertifyInstallation(NewLanternService(graph), replication); err == nil {
		t.Fatal("runtime certified a primary service that bypassed the runtime")
	}
	if runtime.DurableReceiptWAL() {
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
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipts.wal"))
	install := func(t *testing.T, runtime *ServingRuntime) (*LanternService, *LanternReplicationService) {
		t.Helper()
		primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
		replication, err := runtime.NewLanternReplicationService(primary)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.CertifyInstallation(primary, replication); err != nil {
			t.Fatal(err)
		}
		return primary, replication
	}

	fresh, err := CreateDurableReceiptWALServingRuntime(config)
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

	restarted, err := OpenDurableReceiptWALServingRuntime(config)
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
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := durableRuntimeTestConfig(path)
	// Model a wall clock that was previously ahead. Certification must seed
	// the serving HLC above this persisted Store clock even after rollback.
	config.Receipt.ClockHighWater = time.Now().Add(2 * time.Hour)

	fresh, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.DurableReceiptWAL() || fresh.receipt == nil ||
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
	primary := fresh.NewLanternService(nil)
	replication, err := fresh.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.CertifyInstallation(primary, replication); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("durable installation without tombstone policy = %v, want failed precondition", err)
	}
	if primary.receiptEdgeDeleteCoordinator != nil ||
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
				Key:        "wal-restored",
				Expiration: timestamppb.New(time.Now().Add(time.Hour)),
			},
		}},
	})
	// Append without publishing to the live graph. This is the crash point
	// after WAL fsync but before in-memory publication.
	if appended, err := fresh.log.Append(entry.Op, entry.HLC); err != nil || appended.Seq != 1 {
		t.Fatalf("durable append = (%+v, %v), want seq 1", appended, err)
	}
	if _, ok := fresh.graph.GetVertex("wal-restored"); ok {
		t.Fatal("test setup unexpectedly published the WAL-only vertex")
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
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
	if vertex, ok := restarted.graph.GetVertex("wal-restored"); !ok || vertex.GetKey() != "wal-restored" {
		t.Fatalf("WAL-before-publication recovery = %v, %v", vertex, ok)
	}
	if got := restarted.graph.CountByPrefix("wal-"); got != 1 {
		t.Fatalf("rebuilt prefix index count = %d, want 1", got)
	}
	if hits := restarted.graph.SearchVertices("restored", 10, ""); len(hits) != 1 || hits[0].ID != "wal-restored" {
		t.Fatalf("rebuilt search index = %+v, want wal-restored", hits)
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
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipts.wal"))
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

	fresh, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	freshClosed := false
	t.Cleanup(func() {
		if !freshClosed {
			_ = fresh.Close()
		}
	})
	primary := fresh.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replication, err := fresh.NewLanternReplicationService(primary)
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
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary := restarted.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	restartedReplication, err := restarted.NewLanternReplicationService(restartedPrimary)
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

func TestServingRuntimeDurableRejectsLeaseAndGenerationFaults(t *testing.T) {
	t.Run("nonempty fresh target", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "receipts.wal")
		config := durableRuntimeTestConfig(path)
		config.ConfigureGraph = func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			return graph.PutVertex("unexpected", &pb.Vertex{Key: "unexpected"})
		}
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if runtime != nil || err == nil {
			if runtime != nil {
				_ = runtime.Close()
			}
			t.Fatalf("nonempty fresh target = %p, %v", runtime, err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("nonempty target created durable bytes: %v", err)
		}
	})

	t.Run("lease contention", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "receipts.wal")
		config := durableRuntimeTestConfig(path)
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		if second, err := OpenDurableReceiptWALServingRuntime(config); second != nil ||
			!errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
			if second != nil {
				_ = second.Close()
			}
			t.Fatalf("contending runtime = %p, %v", second, err)
		}
	})

	t.Run("missing or corrupt durable artifacts", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*testing.T, string)
		}{
			{
				name: "missing WAL",
				mutate: func(t *testing.T, path string) {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "corrupt WAL",
				mutate: func(t *testing.T, path string) {
					file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := file.Write([]byte("corrupt")); err != nil {
						_ = file.Close()
						t.Fatal(err)
					}
					if err := file.Close(); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "missing tip",
				mutate: func(t *testing.T, path string) {
					if err := os.Remove(path + ".tip"); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "missing clock",
				mutate: func(t *testing.T, path string) {
					if err := os.Remove(path + ".clock"); err != nil {
						t.Fatal(err)
					}
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "receipts.wal")
				config := durableRuntimeTestConfig(path)
				runtime, err := CreateDurableReceiptWALServingRuntime(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.Close(); err != nil {
					t.Fatal(err)
				}
				tc.mutate(t, path)
				if resumed, err := OpenDurableReceiptWALServingRuntime(config); resumed != nil || err == nil {
					if resumed != nil {
						_ = resumed.Close()
					}
					t.Fatalf("faulted artifacts resumed as %p, %v", resumed, err)
				}
				lease, err := mutationlog.AcquireFileWALLease(path)
				if err != nil {
					t.Fatalf("failed artifact validation leaked lease: %v", err)
				}
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})

	t.Run("missing corrupt and mismatched generation", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*testing.T, string)
		}{
			{
				name: "missing",
				mutate: func(t *testing.T, path string) {
					if err := os.Remove(path + ".generation"); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "corrupt",
				mutate: func(t *testing.T, path string) {
					record, err := os.ReadFile(path + ".generation")
					if err != nil {
						t.Fatal(err)
					}
					record[len(record)-1] ^= 0xff
					if err := os.WriteFile(path+".generation", record, 0o600); err != nil {
						t.Fatal(err)
					}
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "receipts.wal")
				config := durableRuntimeTestConfig(path)
				runtime, err := CreateDurableReceiptWALServingRuntime(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.Close(); err != nil {
					t.Fatal(err)
				}
				tc.mutate(t, path)
				if resumed, err := OpenDurableReceiptWALServingRuntime(config); resumed != nil || err == nil {
					if resumed != nil {
						_ = resumed.Close()
					}
					t.Fatalf("faulted generation resumed as %p, %v", resumed, err)
				}
				lease, err := mutationlog.AcquireFileWALLease(path)
				if err != nil {
					t.Fatalf("failed restart leaked lease: %v", err)
				}
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}

		path := filepath.Join(t.TempDir(), "binding.wal")
		config := durableRuntimeTestConfig(path)
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if err != nil {
			t.Fatal(err)
		}
		generation := runtime.receipt.generation
		policy := runtime.receipt.store.PolicyFingerprint()
		canonicalPath := runtime.owner.(*receiptWALOwnedCandidate).lease.Path()
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		wrongEpoch := config.Receipt.Epoch
		wrongEpoch[0] ^= 0xff
		if got, err := readReceiptRuntimeGeneration(canonicalPath, wrongEpoch, policy, config.NodeID); got != ([16]byte{}) ||
			err == nil || !strings.Contains(err.Error(), "binding mismatch") {
			t.Fatalf("wrong-epoch generation = %x, %v", got, err)
		}
		wrongPolicy := policy
		wrongPolicy[0] ^= 0xff
		if got, err := readReceiptRuntimeGeneration(canonicalPath, config.Receipt.Epoch, wrongPolicy, config.NodeID); got != ([16]byte{}) ||
			err == nil || !strings.Contains(err.Error(), "binding mismatch") {
			t.Fatalf("wrong-policy generation = %x, %v", got, err)
		}
		wrongNodeID := config.NodeID
		wrongNodeID[0] ^= 0xff
		if got, err := readReceiptRuntimeGeneration(canonicalPath, config.Receipt.Epoch, policy, wrongNodeID); got != ([16]byte{}) ||
			err == nil || !strings.Contains(err.Error(), "binding mismatch") {
			t.Fatalf("wrong-node generation = %x, %v", got, err)
		}
		if got, err := readReceiptRuntimeGeneration(canonicalPath, config.Receipt.Epoch, policy, config.NodeID); got != generation || err != nil {
			t.Fatalf("valid generation = %x, %v, want %x", got, err, generation)
		}
	})

	t.Run("epoch and policy mismatch", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*DurableReceiptWALRuntimeConfig)
		}{
			{"epoch", func(config *DurableReceiptWALRuntimeConfig) { config.Receipt.Epoch[0] ^= 0xff }},
			{"retention", func(config *DurableReceiptWALRuntimeConfig) { config.Receipt.Retention = 2 * time.Hour }},
			{"entry cap", func(config *DurableReceiptWALRuntimeConfig) { config.Receipt.MaxEntries++ }},
			{"byte cap", func(config *DurableReceiptWALRuntimeConfig) { config.Receipt.MaxBytes++ }},
			{"node ID", func(config *DurableReceiptWALRuntimeConfig) { config.NodeID[0] ^= 0xff }},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "receipts.wal")
				config := durableRuntimeTestConfig(path)
				runtime, err := CreateDurableReceiptWALServingRuntime(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.Close(); err != nil {
					t.Fatal(err)
				}
				clockBefore, err := os.ReadFile(path + ".clock")
				if err != nil {
					t.Fatal(err)
				}
				tc.mutate(&config)
				if resumed, err := OpenDurableReceiptWALServingRuntime(config); resumed != nil || err == nil {
					if resumed != nil {
						_ = resumed.Close()
					}
					t.Fatalf("mismatched restart = %p, %v", resumed, err)
				}
				clockAfter, err := os.ReadFile(path + ".clock")
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(clockAfter, clockBefore) {
					t.Fatal("mismatched restart changed the clock journal before rejecting generation")
				}
				lease, err := mutationlog.AcquireFileWALLease(path)
				if err != nil {
					t.Fatalf("mismatched restart leaked lease: %v", err)
				}
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})

	t.Run("partial fresh failure closes owner", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "receipts.wal")
		if err := os.WriteFile(path+".generation", []byte("uncertain"), 0o600); err != nil {
			t.Fatal(err)
		}
		config := durableRuntimeTestConfig(path)
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if runtime != nil || !errors.Is(err, os.ErrExist) {
			t.Fatalf("partial fresh runtime = %p, %v", runtime, err)
		}
		lease, err := mutationlog.AcquireFileWALLease(path)
		if err != nil {
			t.Fatalf("partial fresh failure leaked lease: %v", err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		if runtime, err := CreateDurableReceiptWALServingRuntime(config); runtime != nil || !errors.Is(err, os.ErrExist) {
			t.Fatalf("partial bytes silently reinitialized: %p, %v", runtime, err)
		}
	})
}
