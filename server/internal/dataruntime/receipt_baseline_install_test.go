package dataruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

type receiptBaselineTestCodec struct {
	raw      []byte
	build    func() (*ReceiptBaselineCandidate, error)
	onEncode func(ReceiptWholeStateCapture)
}

func (c *receiptBaselineTestCodec) EncodeCombinedReceiptBaseline(
	ctx context.Context,
	capture ReceiptWholeStateCapture,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.onEncode != nil {
		c.onEncode(capture)
	}
	return append([]byte(nil), c.raw...), nil
}

func (c *receiptBaselineTestCodec) StageCombinedReceiptBaseline(
	ctx context.Context,
	raw []byte,
	_ mutationreceipt.Config,
	_ time.Duration,
	_ func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (*ReceiptBaselineCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !bytes.Equal(raw, c.raw) {
		return nil, errors.New("noncanonical test baseline")
	}
	return c.build()
}

type receiptBaselineTestImage struct {
	capture ReceiptWholeStateCapture
	codec   *receiptBaselineTestCodec
	id      mutationreceipt.ID
	origin  hlc.NodeID
	cutoff  hlc.Timestamp
}

func setReceiptBaselineTestRetired(
	image *receiptBaselineTestImage,
	state mutationreceipt.RetiredCatalogSnapshot,
) {
	image.capture.Retired = state
	build := image.codec.build
	image.codec.build = func() (*ReceiptBaselineCandidate, error) {
		candidate, err := build()
		if candidate != nil {
			candidate.Retired = state
		}
		return candidate, err
	}
}

func newReceiptBaselineTestImage(t *testing.T, config ReceiptDatabaseConfig) receiptBaselineTestImage {
	t.Helper()
	issued := config.Receipt.ClockHighWater.Truncate(time.Millisecond)
	if issued.IsZero() {
		issued = time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	}
	policy := clearReceiptClockHighWater(config.Receipt)
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	id, err := mutationreceipt.NewID(policy.Epoch, issued, [24]byte{0x91})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID: id, Group: mutationreceipt.GroupID{0x92}, Count: 1,
		Kind: mutationreceipt.PutVertex, Digest: mutationreceipt.IntentDigest([]byte("baseline")),
	}
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	if classification, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil ||
		classification != mutationreceipt.Fresh {
		t.Fatalf("classify baseline receipt = %v, %v", classification, err)
	}
	if err := tx.Reserve([][]byte{[]byte("baseline-result")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	policy.ClockHighWater = snapshot.ClockHighWater()
	retired, err := newEmptyRetiredCatalogSnapshot(policy, snapshot.ClockHighWaterMillis)
	if err != nil {
		t.Fatal(err)
	}
	var origin hlc.NodeID
	for i := range origin {
		origin[i] = 0x88
	}
	cutoff := hlc.Timestamp{WallNs: issued.Add(10 * time.Minute).UnixNano(), Logical: 3, NodeID: origin}
	origins := []OriginState{{Origin: origin, LastSeq: 1, LastHLC: cutoff}}
	build := func() (*ReceiptBaselineCandidate, error) {
		receipts, err := mutationreceipt.NewFromSnapshot(policy, snapshot)
		if err != nil {
			return nil, err
		}
		graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](config.DefaultTTL)
		if err := config.ConfigureGraph(graph); err != nil {
			return nil, err
		}
		expiration := time.Now().Add(time.Hour)
		if !graph.PutVertexWithExpirationHLC(
			"baseline-searchable",
			&pb.Vertex{Key: "baseline-searchable", Value: &pb.Vertex_Nil{Nil: true}, Expiration: timestamppb.New(expiration)},
			expiration,
			cutoff,
		) {
			return nil, errors.New("baseline vertex was rejected")
		}
		if err := graph.CompleteSearchIndexRecovery(); err != nil {
			return nil, err
		}
		return &ReceiptBaselineCandidate{
			Graph: graph, Receipts: receipts, Retired: retired, Policy: policy,
			Origins:        append([]OriginState(nil), origins...),
			CutoffLocalSeq: 41, CutoffHLC: cutoff,
		}, nil
	}
	return receiptBaselineTestImage{
		capture: ReceiptWholeStateCapture{
			Receipts: snapshot, Retired: retired, Policy: policy, Origins: origins,
		},
		codec:  &receiptBaselineTestCodec{raw: []byte("canonical-test-combined-baseline"), build: build},
		id:     id,
		origin: origin,
		cutoff: cutoff,
	}
}

func setReceiptBaselineTestGraphFrames(t *testing.T, image *receiptBaselineTestImage) {
	t.Helper()
	candidate, err := image.codec.build()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := candidate.Graph.SnapshotReplication()
	frames := &receiptSnapshotFrameCollector{}
	err = sendSnapshotFrames(t.Context(), replicationSnapshotCut{
		cutoffPerOrigin: map[string]uint64{fmt.Sprintf("%x", image.origin[:]): 1},
		cutoffHLC:       image.cutoff,
		cutoffLocalSeq:  41,
		barriers:        snapshot.Barriers,
		tombstones:      snapshot.Tombstones,
		graph:           snapshot.Graph,
	}, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT, frames)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceiptSnapshotGraphCapture(frames.frames, image.capture.Origins); err != nil {
		t.Fatal(err)
	}
	image.capture.Graph = frames.frames
}

func baselineRuntimeTestConfig(path string) ReceiptDatabaseConfig {
	now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	return ReceiptDatabaseConfig{
		Path: path, BaselineCodec: &receiptBaselineTestCodec{},
		Receipt: mutationreceipt.Config{
			Epoch: mutationreceipt.Epoch{0x81}, Retention: time.Hour,
			MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: now,
		},
		Log:        mutationlog.Options{Capacity: 8, SubscriberBuffer: 2},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			graph.EnablePrefixIndex(func(key string) string { return key })
			graph.EnableSearchIndex(
				func(key string, _ *pb.Vertex) search.Document { return search.Text(key) },
				strings.Compare,
			)
			return nil
		},
		NodeID: hlc.NodeID{0x82},
		Now:    now,
	}
}

func assertReceiptBaselinePublicationFault(t *testing.T, service *Executor) {
	t.Helper()
	generation, faulted := service.publicationStatus()
	if !faulted {
		t.Fatal("receipt baseline failure did not fault the publication generation")
	}
	select {
	case <-generation:
	default:
		t.Fatal("faulted publication generation remained open")
	}
	if _, err := service.Public().GetVertex(
		context.Background(),
		&pb.GetVertexRequest{Key: "baseline-searchable"},
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("external graph read after receipt baseline failure = %v", err)
	}
}

func TestInstallReceiptBaselineRebindsManagedGraphPolicyAtFinalCut(t *testing.T) {
	config := baselineRuntimeTestConfig(filepath.Join(t.TempDir(), "managed-policy.db"))
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	owner, err := NewGraphPolicyOwner(runtime, primary, GraphPolicyConfig{GCInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	initial := GraphPolicyConfig{GCInterval: time.Hour, GCEdgeBudget: 3, MaxVertices: 1, MaxVertexCausalEntries: 10, MaxEdgeCausalEntries: 10}
	if err := owner.Apply(GraphPolicySnapshot{Generation: 1, Config: initial}); err != nil {
		t.Fatal(err)
	}
	final := initial
	final.GCEdgeBudget, final.MaxVertexCausalEntries, final.MaxEdgeCausalEntries = 0, 1, 1
	changed := false
	runtime.receipt.installFault = func(point receiptBaselineInstallFaultPoint) error {
		if point == receiptBaselineAfterStageBeforeFinalCut && !changed {
			changed = true
			return owner.Apply(GraphPolicySnapshot{Generation: 2, Config: final})
		}
		return nil
	}
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatalf("policy change stranded detached baseline: %v", err)
	}
	if !changed || owner.Snapshot().Generation != 2 {
		t.Fatal("mid-install policy generation was not applied")
	}
	stats := runtime.graph.CausalMetadataStats()
	if stats.MaxVertexEntries != 1 || stats.MaxEdgeEntries != 1 {
		t.Fatalf("installed candidate reverted managed policy: %+v", stats)
	}
	if _, ok := runtime.graph.GetVertex("baseline-searchable"); !ok {
		t.Fatal("managed capacity evicted restored graph")
	}
	if hits := runtime.graph.SearchVertices("baseline", 10, ""); len(hits) == 0 {
		t.Fatal("operational overlay changed the search index")
	}
	candidate := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](config.DefaultTTL)
	if err := runtime.configureGraphCandidate(candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.CausalMetadataStats().MaxVertexEntries != 1 {
		t.Fatal("future configurator retained old bootstrap causal limits")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	// Durable graph/index restoration remains before native policy adoption;
	// installing the verified managed generation afterwards must retain data.
	if _, ok := restarted.graph.GetVertex("baseline-searchable"); !ok {
		t.Fatal("ordinary restart lost baseline data")
	}
	restartedOwner, err := NewGraphPolicyOwner(restarted, restarted.NewExecutor(nil), GraphPolicyConfig{GCInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := restartedOwner.Apply(GraphPolicySnapshot{Generation: 2, Config: final}); err != nil {
		t.Fatal(err)
	}
	if restarted.graph.CausalMetadataStats().MaxVertexEntries != 1 || restartedOwner.Snapshot().Generation != 2 {
		t.Fatal("post-restore managed policy was not effective")
	}
}

func TestInstallReceiptBaselinePristineReceiverClockRebase(t *testing.T) {
	t.Run("prunes expired active and retired evidence at receiver clock", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "graph.db")
		source := baselineRuntimeTestConfig(path)
		source.Receipt.ClockHighWater = time.Now().Add(-3 * time.Hour).Truncate(time.Millisecond)
		source.Now = source.Receipt.ClockHighWater
		image := newReceiptBaselineTestImage(t, source)
		retired, retiredID := mustRetiredCatalogSnapshot(
			t, source.Receipt, image.capture.Receipts.ClockHighWaterMillis, 0x74,
		)
		setReceiptBaselineTestRetired(&image, retired)
		setReceiptBaselineTestGraphFrames(t, &image)
		config := source
		config.Receipt.ClockHighWater = time.Now().Truncate(time.Millisecond)
		config.Now = config.Receipt.ClockHighWater
		config.BaselineCodec = image.codec
		var prepared *ReceiptWholeStateCapture
		image.codec.onEncode = func(capture ReceiptWholeStateCapture) {
			if capture.Receipts.ClockHighWaterMillis == config.Now.UnixMilli() {
				prepared = &capture
			}
		}
		runtime, err := OpenReceiptDatabase(t.Context(), config, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		if err := runtime.NewExecutor(nil).InstallReceiptBaseline(t.Context(), image.capture); err != nil {
			t.Fatal(err)
		}
		if prepared == nil || len(prepared.Receipts.Receipts) != 0 || len(prepared.Retired.Epochs) != 0 ||
			prepared.Receipts.ClockHighWaterMillis != config.Now.UnixMilli() ||
			prepared.Retired.ClockHighWaterMillis != config.Now.UnixMilli() {
			t.Fatalf("expired rows survived rebased sidecar: %+v", prepared)
		}
		if got := runtime.receipt.store.Stats(); got.HighWaterMillis != config.Now.UnixMilli() || got.Entries != 0 {
			t.Fatalf("expired active Store = %+v", got)
		}
		if status, _, err := runtime.receipt.store.Lookup(image.id, config.Now); err != nil ||
			status != mutationreceipt.NoLongerProvable {
			t.Fatalf("expired active proof = %v, %v", status, err)
		}
		observations, err := runtime.receipt.retired.lookupMany(
			runtime.receipt.policy, []mutationreceipt.ID{retiredID}, config.Now,
		)
		if err != nil || len(observations) != 1 || observations[0].Status != mutationreceipt.NoLongerProvable {
			t.Fatalf("expired retired proof = %+v, %v", observations, err)
		}
		if _, ok := runtime.graph.GetVertex("baseline-searchable"); !ok {
			t.Fatal("receipt expiry removed source graph state")
		}
	})

	t.Run("rechecks causal evidence at final publication cut", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "graph.db")
		source := baselineRuntimeTestConfig(path)
		source.Receipt.ClockHighWater = time.Now().Add(-20 * time.Minute).Truncate(time.Millisecond)
		source.Now = source.Receipt.ClockHighWater
		image := newReceiptBaselineTestImage(t, source)
		config := source
		config.Receipt.ClockHighWater = source.Now.Add(20 * time.Minute)
		config.Now = config.Receipt.ClockHighWater
		config.BaselineCodec = image.codec
		runtime, err := OpenReceiptDatabase(t.Context(), config, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		hookCalls := 0
		runtime.receipt.installFault = func(point receiptBaselineInstallFaultPoint) error {
			if point == receiptBaselineAfterStageBeforeFinalCut && hookCalls == 0 {
				hookCalls++
				if !runtime.graph.ApplyVertexCausalBarrierHLC("expired-put", hlc.Timestamp{
					WallNs: config.Now.UnixNano(), NodeID: hlc.NodeID{0x93},
				}) {
					t.Fatal("failed to install causal evidence")
				}
			}
			return nil
		}
		if err := runtime.NewExecutor(nil).InstallReceiptBaseline(t.Context(), image.capture); !errors.Is(
			err, mutationreceipt.ErrRetiredCatalogClockRollback,
		) {
			t.Fatalf("new causal evidence accepted a receiver clock rollback: %v", err)
		}
		if hookCalls != 1 || runtime.graph.VertexCount() != 0 ||
			len(runtime.graph.SnapshotReplication().Barriers.Vertices) != 1 {
			t.Fatalf("final-cut causal barrier = %d hooks, %+v", hookCalls, runtime.graph.SnapshotReplication().Barriers)
		}
		if _, ok := runtime.log.LastSeq(); ok {
			t.Fatal("rejected baseline committed a marker")
		}
	})

}

func TestInstallReceiptBaselineRetiredStateInvariants(t *testing.T) {
	t.Run("unions retired catalog without replacing slot", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "graph.db")
		config := baselineRuntimeTestConfig(path)
		image := newReceiptBaselineTestImage(t, config)
		highWater := image.capture.Receipts.ClockHighWaterMillis
		incoming, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x41)
		local, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x42)
		setReceiptBaselineTestRetired(&image, incoming)
		config.BaselineCodec = image.codec
		runtime, err := OpenReceiptDatabase(t.Context(), config, true)
		if err != nil {
			t.Fatal(err)
		}

		t.Run("retries when retired catalog advances during encoding", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "graph.db")
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			highWater := image.capture.Receipts.ClockHighWaterMillis
			incoming, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x43)
			local, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x44)
			setReceiptBaselineTestRetired(&image, incoming)
			config.BaselineCodec = image.codec
			runtime, err := OpenReceiptDatabase(t.Context(), config, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			primary := runtime.NewExecutor(nil)
			hookCalls := 0
			runtime.receipt.installFault = func(point receiptBaselineInstallFaultPoint) error {
				if point == receiptBaselineAfterStageBeforeFinalCut && hookCalls == 0 {
					hookCalls++
					mustReplaceRetiredCatalog(
						t,
						runtime.receipt.retired,
						runtime.receipt.policy,
						highWater,
						local,
					)
				}
				return nil
			}

			if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
				t.Fatal(err)
			}
			if hookCalls != 1 {
				t.Fatalf("retired drift hook calls = %d, want 1", hookCalls)
			}
			got, _, err := runtime.receipt.retired.snapshot(runtime.receipt.policy, highWater)
			if err != nil || len(got.Epochs) != 2 {
				t.Fatalf("retried retired union = %+v, %v", got, err)
			}
			if last, ok := runtime.log.LastSeq(); !ok || last != 1 {
				t.Fatalf("retry marker count = %d, %t, want 1, true", last, ok)
			}
		})
		t.Cleanup(func() { _ = runtime.Close() })
		slot := runtime.receipt.retired
		mustReplaceRetiredCatalog(t, slot, runtime.receipt.policy, highWater, local)
		primary := runtime.NewExecutor(nil)

		if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
			t.Fatal(err)
		}
		if runtime.receipt.retired != slot || primary.receiptRetiredCatalog != slot {
			t.Fatal("retired catalog slot identity changed during install")
		}
		got, _, err := slot.snapshot(runtime.receipt.policy, highWater)
		if err != nil {
			t.Fatal(err)
		}
		configured, _, err := retiredCatalogConfig(runtime.receipt.policy, highWater)
		if err != nil {
			t.Fatal(err)
		}
		wantCatalog, err := mutationreceipt.NewRetiredCatalogFromUnion(configured, incoming, local)
		if err != nil {
			t.Fatal(err)
		}
		want, err := wantCatalog.Snapshot(time.UnixMilli(highWater))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) || len(got.Epochs) != 2 {
			t.Fatalf("retired union = %+v, want %+v", got, want)
		}
		if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
			t.Fatalf("idempotent retired union: %v", err)
		}
		again, _, err := slot.snapshot(runtime.receipt.policy, highWater)
		if err != nil || !reflect.DeepEqual(again, want) {
			t.Fatalf("idempotent retired union = %+v, %v", again, err)
		}
	})

	t.Run("rejects invalid retired union before marker", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			maxEntries int
			incoming   func(
				t *testing.T,
				policy mutationreceipt.Config,
				highWater int64,
				local mutationreceipt.RetiredCatalogSnapshot,
			) mutationreceipt.RetiredCatalogSnapshot
			want error
		}{
			{
				name: "conflict",
				incoming: func(
					_ *testing.T,
					_ mutationreceipt.Config,
					_ int64,
					local mutationreceipt.RetiredCatalogSnapshot,
				) mutationreceipt.RetiredCatalogSnapshot {
					conflict := local
					conflict.Epochs = append([]mutationreceipt.RetiredEpochSnapshot(nil), local.Epochs...)
					conflict.Epochs[0].State.Receipts = append(
						[]mutationreceipt.Receipt(nil),
						local.Epochs[0].State.Receipts...,
					)
					conflict.Epochs[0].State.Receipts[0].Result = []byte("conflict")
					return conflict
				},
				want: mutationreceipt.ErrInvalidRetiredCatalogSnapshot,
			},
			{
				name:       "aggregate capacity",
				maxEntries: 1,
				incoming: func(
					t *testing.T,
					policy mutationreceipt.Config,
					highWater int64,
					_ mutationreceipt.RetiredCatalogSnapshot,
				) mutationreceipt.RetiredCatalogSnapshot {
					state, _ := mustRetiredCatalogSnapshot(t, policy, highWater, 0x52)
					return state
				},
				want: mutationreceipt.ErrRetiredCatalogCapacity,
			},
			{
				name: "active epoch",
				incoming: func(
					_ *testing.T,
					policy mutationreceipt.Config,
					_ int64,
					local mutationreceipt.RetiredCatalogSnapshot,
				) mutationreceipt.RetiredCatalogSnapshot {
					state := local
					state.Epochs = append([]mutationreceipt.RetiredEpochSnapshot(nil), local.Epochs...)
					state.Epochs[0].Policy.Epoch = policy.Epoch
					state.Epochs[0].State.Epoch = policy.Epoch
					return state
				},
				want: mutationreceipt.ErrActiveEpochReceipt,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "graph.db")
				config := baselineRuntimeTestConfig(path)
				if tc.maxEntries != 0 {
					config.Receipt.MaxEntries = tc.maxEntries
				}
				image := newReceiptBaselineTestImage(t, config)
				highWater := image.capture.Receipts.ClockHighWaterMillis
				local, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x51)
				setReceiptBaselineTestRetired(
					&image,
					tc.incoming(t, config.Receipt, highWater, local),
				)
				config.BaselineCodec = image.codec
				runtime, err := OpenReceiptDatabase(t.Context(), config, true)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = runtime.Close() })
				mustReplaceRetiredCatalog(
					t,
					runtime.receipt.retired,
					runtime.receipt.policy,
					highWater,
					local,
				)
				before, _, err := runtime.receipt.retired.snapshot(runtime.receipt.policy, highWater)
				if err != nil {
					t.Fatal(err)
				}
				primary := runtime.NewExecutor(nil)
				if err := primary.InstallReceiptBaseline(t.Context(), image.capture); !errors.Is(err, tc.want) {
					t.Fatalf("invalid retired union = %v, want %v", err, tc.want)
				}
				after, _, err := runtime.receipt.retired.snapshot(runtime.receipt.policy, highWater)
				if err != nil || !reflect.DeepEqual(after, before) {
					t.Fatalf("rejected retired union changed local state: before=%+v after=%+v err=%v", before, after, err)
				}
				if _, ok := runtime.log.LastSeq(); ok {
					t.Fatal("rejected retired union committed a marker")
				}
			})
		}
	})

	t.Run("rejects active high-water rollback", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "graph.db")
		config := baselineRuntimeTestConfig(path)
		image := newReceiptBaselineTestImage(t, config)
		if len(image.capture.Receipts.Receipts) != 1 {
			t.Fatalf("receipt fixture rows = %d, want 1", len(image.capture.Receipts.Receipts))
		}
		deadline := image.capture.Receipts.Receipts[0].DeadlineMillis
		initialHighWater := deadline - int64(2*time.Minute/time.Millisecond)
		sourceHighWater := deadline - int64(time.Minute/time.Millisecond)
		effectiveHighWater := deadline + int64(time.Minute/time.Millisecond)
		config.Receipt.ClockHighWater = time.UnixMilli(initialHighWater)
		config.Now = time.UnixMilli(initialHighWater)

		baseBuild := image.codec.build
		buildAtHighWater := func(highWater int64, includeReceipt bool) func() (*ReceiptBaselineCandidate, error) {
			return func() (*ReceiptBaselineCandidate, error) {
				candidate, err := baseBuild()
				if err != nil {
					return nil, err
				}
				state, err := candidate.Receipts.Snapshot()
				if err != nil {
					return nil, err
				}
				state.ClockHighWaterMillis = highWater
				if !includeReceipt {
					state.Receipts = nil
				}
				policy := candidate.Policy
				policy.ClockHighWater = time.UnixMilli(highWater)
				candidate.Receipts, err = mutationreceipt.NewFromSnapshot(policy, state)
				if err != nil {
					return nil, err
				}
				retired, err := newEmptyRetiredCatalogSnapshot(policy, highWater)
				if err != nil {
					return nil, err
				}
				candidate.Retired = retired
				candidate.Policy = policy
				return candidate, err
			}
		}

		firstCapture := image.capture
		firstCapture.Receipts.ClockHighWaterMillis = effectiveHighWater
		firstCapture.Receipts.Receipts = nil
		firstCapture.Policy.ClockHighWater = time.UnixMilli(effectiveHighWater)
		firstRetired, err := newEmptyRetiredCatalogSnapshot(firstCapture.Policy, effectiveHighWater)
		if err != nil {
			t.Fatal(err)
		}
		firstCapture.Retired = firstRetired
		firstCodec := &receiptBaselineTestCodec{
			raw:   []byte("canonical-high-water-baseline-a"),
			build: buildAtHighWater(effectiveHighWater, false),
		}
		secondCapture := image.capture
		secondCapture.Receipts.ClockHighWaterMillis = sourceHighWater
		secondCapture.Policy.ClockHighWater = time.UnixMilli(sourceHighWater)
		secondRetired, err := newEmptyRetiredCatalogSnapshot(secondCapture.Policy, sourceHighWater)
		if err != nil {
			t.Fatal(err)
		}
		secondCapture.Retired = secondRetired
		secondCodec := &receiptBaselineTestCodec{
			raw:   []byte("canonical-high-water-baseline-b"),
			build: buildAtHighWater(sourceHighWater, true),
		}

		config.BaselineCodec = firstCodec
		runtime, err := OpenReceiptDatabase(t.Context(), config, true)
		if err != nil {
			t.Fatal(err)
		}
		primary := runtime.NewExecutor(nil)
		if err := primary.InstallReceiptBaseline(context.Background(), firstCapture); err != nil {
			t.Fatal(err)
		}
		if got := runtime.receipt.store.Stats(); got.HighWaterMillis != effectiveHighWater || got.Entries != 0 {
			t.Fatalf("first baseline Store = %+v", got)
		}

		runtime.receipt.baselineCodec = secondCodec
		if err := primary.InstallReceiptBaseline(context.Background(), secondCapture); !errors.Is(
			err,
			mutationreceipt.ErrRetiredCatalogClockRollback,
		) {
			t.Fatalf("high-water rollback = %v", err)
		}
		if got := runtime.receipt.store.Stats(); got.HighWaterMillis != effectiveHighWater || got.Entries != 0 {
			t.Fatalf("second baseline regressed live Store = %+v", got)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}

		config.BaselineCodec = firstCodec
		config.Now = time.UnixMilli(initialHighWater)
		config.Receipt.ClockHighWater = time.UnixMilli(initialHighWater)
		restarted, err := OpenReceiptDatabase(t.Context(), config, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = restarted.Close() })
		if got := restarted.receipt.store.Stats(); got.HighWaterMillis != effectiveHighWater || got.Entries != 0 {
			t.Fatalf("restarted Store = %+v, want high-water %d and no resurrected rows", got, effectiveHighWater)
		}
		if status, _, err := restarted.receipt.store.Lookup(
			image.id,
			time.UnixMilli(initialHighWater),
		); err != nil || status != mutationreceipt.NoLongerProvable {
			t.Fatalf("expired receipt after restart = %v, %v", status, err)
		}
	})
}

func TestInstallReceiptBaselineCrashPointsRecoverOrRemainUncommitted(t *testing.T) {
	for _, point := range []receiptBaselineInstallFaultPoint{
		receiptBaselineAfterMarkerBeforePublish,
		receiptBaselineAfterPublish,
	} {
		t.Run(string(point), func(t *testing.T) {
			path := t.TempDir() + "/graph.db"
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			highWater := image.capture.Receipts.ClockHighWaterMillis
			retiredState, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x71)
			setReceiptBaselineTestRetired(&image, retiredState)
			config.BaselineCodec = image.codec
			runtime, err := OpenReceiptDatabase(t.Context(), config, true)
			if err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewExecutor(nil)
			runtime.receipt.installFault = func(got receiptBaselineInstallFaultPoint) error {
				if got == point {
					return fmt.Errorf("injected %s crash", point)
				}
				return nil
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Errorf("%s did not panic", point)
					}
				}()
				_ = primary.InstallReceiptBaseline(context.Background(), image.capture)
			}()
			if point == receiptBaselineAfterMarkerBeforePublish {
				if !primary.receiptCommitFaulted {
					t.Fatal("interrupted marker publication did not fail-stop service")
				}
				if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
					t.Fatal("pre-publication crash exposed baseline graph")
				}
				got, _, err := runtime.receipt.retired.snapshot(runtime.receipt.policy, highWater)
				if err != nil || len(got.Epochs) != 0 {
					t.Fatalf("pre-publication crash exposed retired catalog: %+v, %v", got, err)
				}
			} else {
				if _, ok := runtime.graph.GetVertex("baseline-searchable"); !ok {
					t.Fatal("post-publication crash lost installed graph")
				}
				got, _, err := runtime.receipt.retired.snapshot(runtime.receipt.policy, highWater)
				if err != nil || !reflect.DeepEqual(got, retiredState) {
					t.Fatalf("post-publication retired catalog = %+v, %v", got, err)
				}
			}
			assertReceiptBaselinePublicationFault(t, primary)
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, err := OpenReceiptDatabase(t.Context(), config, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := restarted.graph.GetVertex("baseline-searchable"); !ok {
				t.Fatal("restart did not recover committed baseline")
			}
			got, _, err := restarted.receipt.retired.snapshot(restarted.receipt.policy, highWater)
			if err != nil || !reflect.DeepEqual(got, retiredState) {
				t.Fatalf("restart retired catalog = %+v, %v", got, err)
			}
			if err := restarted.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallReceiptBaselineRejectsPolicyAndOriginRegressionBeforeMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewExecutor(nil)
	generation := runtime.receipt.generation

	wrongPolicy := image.capture
	wrongPolicy.Receipts.PolicyFingerprint[0] ^= 1
	if err := primary.InstallReceiptBaseline(context.Background(), wrongPolicy); err == nil {
		t.Fatal("policy-mismatched baseline installed")
	}
	if _, ok := runtime.log.LastSeq(); ok {
		t.Fatal("policy rejection committed a marker")
	}

	newer := image.cutoff
	newer.WallNs++
	if !runtime.origins.Record(image.origin, 1, image.cutoff) ||
		!runtime.origins.Record(image.origin, 2, newer) {
		t.Fatal("failed to seed newer local origin state")
	}
	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err == nil {
		t.Fatal("origin-regressing baseline installed")
	}
	if _, ok := runtime.log.LastSeq(); ok {
		t.Fatal("origin rejection committed a marker")
	}
	if runtime.receipt.generation != generation {
		t.Fatal("rejected baseline rotated generation")
	}
	if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
		t.Fatal("rejected baseline published graph")
	}
}

func TestInstallReceiptBaselineRejectsRetainedReceiptRegressionBeforeMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewExecutor(nil)
	generation := runtime.receipt.generation

	issued := config.Now.Add(10 * time.Second)
	id, err := mutationreceipt.NewID(config.Receipt.Epoch, issued, [24]byte{0x44})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID: id, Group: mutationreceipt.GroupID{0x45}, Count: 1,
		Kind: mutationreceipt.PutVertex, Digest: mutationreceipt.IntentDigest([]byte("receiver-only")),
	}
	tx, err := runtime.receipt.store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	if classification, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil ||
		classification != mutationreceipt.Fresh {
		tx.Abort()
		t.Fatalf("classify receiver receipt = %v, %v", classification, err)
	}
	if err := tx.Reserve([][]byte{[]byte("receiver-result")}); err != nil {
		tx.Abort()
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		tx.Abort()
		t.Fatal(err)
	}
	tx.Commit()
	highWater := runtime.receipt.store.Stats().HighWaterMillis
	image.capture.Receipts.ClockHighWaterMillis = highWater
	image.capture.Policy.ClockHighWater = time.UnixMilli(highWater)
	image.capture.Retired, err = newEmptyRetiredCatalogSnapshot(image.capture.Policy, highWater)
	if err != nil {
		t.Fatal(err)
	}
	build := image.codec.build
	image.codec.build = func() (*ReceiptBaselineCandidate, error) {
		candidate, err := build()
		if err != nil {
			return nil, err
		}
		state, err := candidate.Receipts.Snapshot()
		if err != nil {
			return nil, err
		}
		state.ClockHighWaterMillis = highWater
		policy := candidate.Policy
		policy.ClockHighWater = time.UnixMilli(highWater)
		candidate.Receipts, err = mutationreceipt.NewFromSnapshot(policy, state)
		if err != nil {
			return nil, err
		}
		candidate.Retired, err = newEmptyRetiredCatalogSnapshot(policy, highWater)
		candidate.Policy = policy
		return candidate, err
	}

	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); !errors.Is(err, mutationreceipt.ErrSnapshotDoesNotDominate) {
		t.Fatalf("receipt-regressing baseline error = %v", err)
	}
	if _, ok := runtime.log.LastSeq(); ok {
		t.Fatal("receipt-regressing baseline committed a marker")
	}
	if runtime.receipt.generation != generation {
		t.Fatal("receipt-regressing baseline rotated generation")
	}
	if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
		t.Fatal("receipt-regressing baseline published graph")
	}
	if status, receipt, err := runtime.receipt.store.Lookup(id, issued); err != nil ||
		status != mutationreceipt.Confirmed || string(receipt.Result) != "receiver-result" {
		t.Fatalf("rejected baseline lost receiver receipt: %v, %+v, %v", status, receipt, err)
	}
}

func TestInstallReceiptBaselineCancellationBeforeMarkerRollsBackStages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewExecutor(nil)
	generation := runtime.receipt.generation

	primary.receiptOriginCutMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	installDone := make(chan error, 1)
	go func() {
		installDone <- primary.InstallReceiptBaseline(ctx, image.capture)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if !primary.replicationCutMu.TryRLock() {
			break
		}
		primary.replicationCutMu.RUnlock()
		if !time.Now().Before(deadline) {
			primary.receiptOriginCutMu.Unlock()
			cancel()
			t.Fatal("baseline install did not reach the exclusive publication cut")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-installDone:
		primary.receiptOriginCutMu.Unlock()
		cancel()
		t.Fatalf("baseline install returned before the blocked origin cut was released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	cancel()
	primary.receiptOriginCutMu.Unlock()
	select {
	case err := <-installDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled baseline install = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled baseline install remained blocked")
	}
	if _, ok := runtime.log.LastSeq(); ok {
		t.Fatal("canceled baseline install committed a marker")
	}
	if runtime.receipt.generation != generation {
		t.Fatal("canceled baseline install rotated generation")
	}
	if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
		t.Fatal("canceled baseline install published graph")
	}
	if got := runtime.receipt.store.Stats(); got.Entries != 0 {
		t.Fatalf("canceled baseline install changed Store = %+v", got)
	}
	if states := runtime.origins.States(); len(states) != 0 {
		t.Fatalf("canceled baseline install changed origins = %+v", states)
	}
	faultGeneration, faulted := primary.publicationStatus()
	if faulted {
		t.Fatal("definitely canceled baseline install faulted publication")
	}
	select {
	case <-faultGeneration:
		t.Fatal("definitely canceled baseline install closed publication generation")
	default:
	}
}

func TestInstallReceiptBaselineConcurrentGenerationChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil)
	genesis := runtime.receipt.generation

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- primary.InstallReceiptBaseline(context.Background(), image.capture)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent baseline install: %v", err)
		}
	}
	active := runtime.receipt.generation
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenReceiptDatabase(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	tip, ok := restarted.log.LastSeq()
	if active == genesis || restarted.receipt.generation != active || !ok || tip != 2 {
		t.Fatalf("concurrent installs: generation %x, tip %d", restarted.receipt.generation, tip)
	}
}

type blockingReceiptBaselineCodec struct {
	delegate ReceiptBaselineArchiveCodec
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (c *blockingReceiptBaselineCodec) EncodeCombinedReceiptBaseline(
	ctx context.Context,
	capture ReceiptWholeStateCapture,
) ([]byte, error) {
	c.once.Do(func() { close(c.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.release:
	}
	return c.delegate.EncodeCombinedReceiptBaseline(ctx, capture)
}

func (c *blockingReceiptBaselineCodec) StageCombinedReceiptBaseline(
	ctx context.Context,
	raw []byte,
	config mutationreceipt.Config,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (*ReceiptBaselineCandidate, error) {
	return c.delegate.StageCombinedReceiptBaseline(ctx, raw, config, defaultTTL, configureGraph)
}

func TestInstallReceiptBaselineExcludesPublicReceiptOperations(t *testing.T) {
	path := t.TempDir() + "/graph.db"
	config := baselineRuntimeTestConfig(path)
	config.Receipt.ClockHighWater = time.Now().Add(time.Hour).Truncate(time.Millisecond)
	config.Now = config.Receipt.ClockHighWater
	image := newReceiptBaselineTestImage(t, config)
	blocking := &blockingReceiptBaselineCodec{
		delegate: image.codec,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	config.BaselineCodec = blocking
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyReceiptBackup(primary, replication); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(primary, replication); err != nil {
		t.Fatal(err)
	}
	before, err := primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || !before.GetEnabled() {
		t.Fatalf("capability before install = %+v, %v", before, err)
	}

	installDone := make(chan error, 1)
	go func() {
		installDone <- primary.InstallReceiptBaseline(context.Background(), image.capture)
	}()
	waitReceiptTest(t, "exclusive baseline install", blocking.entered)

	during, err := primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || during.GetEnabled() || during.GetPolicy() != nil || during.GetEndpoint() != nil {
		t.Fatalf("capability during install = %+v, %v", during, err)
	}
	if _, err := primary.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: image.id.Bytes(),
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("status during install = %v, want FailedPrecondition", err)
	}
	if _, err := primary.DeleteEdge(context.Background(), &pb.DeleteEdgeRequest{
		Tail: "blocked", Head: "during-install",
		ReceiptContext: &pb.MutationReceiptContext{
			OperationIds:  [][]byte{image.id.Bytes()},
			LogicalCallId: []byte{0x93, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
			Endpoint:      before.GetEndpoint(),
		},
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("receipt mutation during install = %v, want FailedPrecondition", err)
	}

	close(blocking.release)
	if err := waitReceiptTest(t, "baseline install completion", installDone); err != nil {
		t.Fatal(err)
	}
	after, err := primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || !after.GetEnabled() ||
		bytes.Equal(after.GetEndpoint().GetGeneration(), before.GetEndpoint().GetGeneration()) {
		t.Fatalf("capability after install = %+v, %v", after, err)
	}
}
