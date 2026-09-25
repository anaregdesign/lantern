package service

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReceiptBaselineRecoveryRejectsMissingCorruptAndMismatchedState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, path string, config DurableReceiptWALRuntimeConfig, image receiptBaselineTestImage)
	}{
		{
			name: "missing sidecar",
			damage: func(t *testing.T, path string, _ DurableReceiptWALRuntimeConfig, image receiptBaselineTestImage) {
				t.Helper()
				digest := sha256.Sum256(image.codec.raw)
				if err := os.Remove(receiptBaselineSidecarPath(path, ReceiptBaselineFormatCombinedV2, digest)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt sidecar",
			damage: func(t *testing.T, path string, _ DurableReceiptWALRuntimeConfig, image receiptBaselineTestImage) {
				t.Helper()
				digest := sha256.Sum256(image.codec.raw)
				if err := os.WriteFile(receiptBaselineSidecarPath(path, ReceiptBaselineFormatCombinedV2, digest), []byte("truncated"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "archive provenance mismatch",
			damage: func(t *testing.T, _ string, _ DurableReceiptWALRuntimeConfig, image receiptBaselineTestImage) {
				t.Helper()
				original := image.codec.build
				image.codec.build = func() (*ReceiptBaselineCandidate, error) {
					candidate, err := original()
					if candidate != nil {
						candidate.CutoffLocalSeq++
					}
					return candidate, err
				}
			},
		},
		{
			name: "receipt high-water mismatch",
			damage: func(t *testing.T, _ string, _ DurableReceiptWALRuntimeConfig, image receiptBaselineTestImage) {
				t.Helper()
				original := image.codec.build
				image.codec.build = func() (*ReceiptBaselineCandidate, error) {
					candidate, err := original()
					if err != nil {
						return nil, err
					}
					state, err := candidate.Receipts.Snapshot()
					if err != nil {
						return nil, err
					}
					state.ClockHighWaterMillis++
					policy := candidate.Policy
					policy.ClockHighWater = time.UnixMilli(state.ClockHighWaterMillis)
					candidate.Receipts, err = mutationreceipt.NewFromSnapshot(policy, state)
					candidate.Policy = policy
					return candidate, err
				}
			},
		},
		{
			name: "forged later marker high-water and restore floor",
			damage: func(t *testing.T, path string, config DurableReceiptWALRuntimeConfig, _ receiptBaselineTestImage) {
				t.Helper()
				scan, err := scanReceiptBaselineWAL(path, config.Receipt, config.NodeID)
				if err != nil {
					t.Fatal(err)
				}
				marker := scan.marker
				marker.ReceiptHighWaterMillis++
				marker.RestoreFloor.WallNs += int64(time.Millisecond)
				marker.RestoreFloor.Logical = 0
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path + ".tip"); err != nil {
					t.Fatal(err)
				}
				store, err := mutationreceipt.New(config.Receipt)
				if err != nil {
					t.Fatal(err)
				}
				log, owner, err := mutationlog.CreateLeasedLogWithFileWALTip(
					path,
					config.Log,
					encodeReceiptWALUnion,
					receiptWALTipBinding(config.Receipt.Epoch, store.PolicyFingerprint()),
				)
				if err != nil {
					t.Fatal(err)
				}
				if entry, err := log.Append(marker, marker.RestoreFloor); err != nil || entry.Seq != 1 {
					_ = owner.Close()
					t.Fatalf("write forged marker = %+v, %v", entry, err)
				}
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "genesis generation mismatch",
			damage: func(t *testing.T, path string, config DurableReceiptWALRuntimeConfig, _ receiptBaselineTestImage) {
				t.Helper()
				store, err := mutationreceipt.New(config.Receipt)
				if err != nil {
					t.Fatal(err)
				}
				record := encodeReceiptRuntimeGeneration(
					path, config.Receipt.Epoch, store.PolicyFingerprint(), config.NodeID, [16]byte{0xee},
				)
				if err := os.WriteFile(path+".generation", record, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.wal")
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewLanternService(nil)
			if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			tc.damage(t, path, config, image)
			if restarted, err := OpenDurableReceiptWALServingRuntime(config); restarted != nil || err == nil {
				if restarted != nil {
					_ = restarted.Close()
				}
				t.Fatalf("damaged committed baseline reopened: runtime=%p err=%v", restarted, err)
			}
		})
	}
}

func TestReceiptBaselineRecoveryNeverFallsBackFromNewestMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	firstDigest := sha256.Sum256(image.codec.raw)
	image.codec.raw = []byte("canonical-test-receipt-baseline-v2")
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	secondDigest := sha256.Sum256(image.codec.raw)
	if firstDigest == secondDigest {
		t.Fatal("test baselines unexpectedly share a digest")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(receiptBaselineSidecarPath(path, ReceiptBaselineFormatCombinedV2, firstDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("older committed sidecar was not reaped: %v", err)
	}
	if err := os.WriteFile(receiptBaselineSidecarPath(path, ReceiptBaselineFormatCombinedV2, secondDigest), []byte("bad newest"), 0o600); err != nil {
		t.Fatal(err)
	}
	if restarted, err := OpenDurableReceiptWALServingRuntime(config); restarted != nil || err == nil {
		if restarted != nil {
			_ = restarted.Close()
		}
		t.Fatalf("recovery fell back from corrupt newest marker: runtime=%p err=%v", restarted, err)
	}
}

func TestReceiptBaselineRecoveryValidatesWALBeforeAndAfterMarker(t *testing.T) {
	for _, corruptFrame := range []int{0, 2} {
		t.Run(map[int]string{0: "before marker", 2: "after marker"}[corruptFrame], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.wal")
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			before := recoveryGraphPutEffectEntry(t, 0x77, image.cutoff.WallNs-1, &pb.MutationOp{
				Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "before"}}},
			})
			if _, err := runtime.log.Append(before.Op, before.HLC); err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewLanternService(nil)
			if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
				t.Fatal(err)
			}
			after := recoveryGraphPutEffectEntry(t, 0x88, image.cutoff.WallNs+1, &pb.MutationOp{
				Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "after"}}},
			})
			after.Op.(*graphPutEffectEnvelope).Mutation.Seq = 2
			if _, err := runtime.log.Append(after.Op, after.HLC); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			offsets := receiptBaselineTestFrameOffsets(t, raw)
			if len(offsets) != 3 {
				t.Fatalf("WAL frame count = %d, want 3", len(offsets))
			}
			raw[offsets[corruptFrame]+4] ^= 1 // Corrupt the selected frame CRC.
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if restarted, err := OpenDurableReceiptWALServingRuntime(config); restarted != nil || err == nil {
				if restarted != nil {
					_ = restarted.Close()
				}
				t.Fatalf("corrupt WAL reopened: runtime=%p err=%v", restarted, err)
			}
		})
	}
}

func TestReceiptBaselineMarkerScanEnforcesGenerationAndPolicyChain(t *testing.T) {
	config := baselineRuntimeTestConfig(filepath.Join(t.TempDir(), "unused"))
	store, err := mutationreceipt.New(config.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	first := receiptBaselineMarkerFixture()
	first.Epoch = config.Receipt.Epoch
	first.PolicyFingerprint = store.PolicyFingerprint()
	first.RestoreFloor.NodeID = config.NodeID
	first.PreviousGeneration = [16]byte{1}
	first.RotatedGeneration = [16]byte{2}
	second := first
	second.PreviousGeneration = first.RotatedGeneration
	second.RotatedGeneration = [16]byte{3}
	advanced := second
	advanced.ReceiptHighWaterMillis++
	advanced.SnapshotHLC.WallNs += 2_000_000
	advanced.RestoreFloor.WallNs += 2_000_000
	for _, tc := range []struct {
		name       string
		second     receiptBaselineMarker
		wantErr    string
		wantActive [16]byte
	}{
		{name: "equal durability frontiers", second: second, wantActive: second.RotatedGeneration},
		{name: "advancing durability frontiers", second: advanced, wantActive: advanced.RotatedGeneration},
		{name: "broken chain", second: func() receiptBaselineMarker {
			bad := second
			bad.PreviousGeneration = [16]byte{9}
			return bad
		}(), wantErr: "marker generation chain mismatch"},
		{name: "policy mismatch", second: func() receiptBaselineMarker {
			bad := second
			bad.PolicyFingerprint[0] ^= 1
			return bad
		}(), wantErr: "marker policy, epoch, or local node mismatch"},
		{name: "regressing receipt high-water", second: func() receiptBaselineMarker {
			bad := second
			bad.ReceiptHighWaterMillis--
			return bad
		}(), wantErr: "marker receipt high-water regressed"},
		{name: "regressing restore floor", second: func() receiptBaselineMarker {
			bad := second
			bad.SnapshotHLC.WallNs = first.ReceiptHighWaterMillis * int64(time.Millisecond)
			bad.RestoreFloor.WallNs = bad.SnapshotHLC.WallNs
			bad.RestoreFloor.Logical = bad.SnapshotHLC.Logical + 1
			return bad
		}(), wantErr: "marker restore floor regressed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "markers.wal")
			wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
			if err != nil {
				t.Fatal(err)
			}
			if err := wal.Write(mutationlog.Entry{Seq: 1, HLC: first.RestoreFloor, Op: first}); err != nil {
				t.Fatal(err)
			}
			if err := wal.Write(mutationlog.Entry{Seq: 2, HLC: tc.second.RestoreFloor, Op: tc.second}); err != nil {
				t.Fatal(err)
			}
			if err := wal.Close(); err != nil {
				t.Fatal(err)
			}
			scan, err := scanReceiptBaselineWAL(path, config.Receipt, config.NodeID)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("invalid marker chain selected %+v, err = %v, want %q", scan, err, tc.wantErr)
				}
				return
			}
			if err != nil || !scan.hasMarker || scan.markerSequence != 2 ||
				scan.activeGeneration != tc.wantActive {
				t.Fatalf("marker scan = %+v, %v", scan, err)
			}
		})
	}
}

func TestReceiptBaselineSuffixCoalescesExactReceiptDuplicatesAndRejectsConflicts(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "exact duplicate"
		if conflict {
			name = "conflicting duplicate"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.wal")
			config := baselineRuntimeTestConfig(path)
			config.Receipt.MaxEntries = 2
			image := newReceiptBaselineTestImage(t, config)
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewLanternService(nil)
			if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
				t.Fatal(err)
			}
			first := receiptBaselineSuffixEnvelope(
				t, config.Receipt, 0x91, 1, image.cutoff.WallNs+int64(time.Millisecond),
				graphcache.EdgeKey[string]{Tail: "duplicate", Head: "same"}, nil,
			)
			secondKey := first.OriginalKeys[0]
			if conflict {
				secondKey.Head = "conflict"
			}
			second := receiptBaselineSuffixEnvelope(
				t, config.Receipt, 0x92, 1, first.HLC.WallNs+int64(time.Millisecond),
				secondKey, &first.Receipts[0],
			)
			for _, envelope := range []*edgeDeleteReceiptEnvelope{first, second} {
				if _, err := runtime.log.Append(envelope, envelope.HLC); err != nil {
					t.Fatal(err)
				}
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}

			restarted, err := OpenDurableReceiptWALServingRuntime(config)
			if conflict {
				if restarted != nil {
					_ = restarted.Close()
				}
				if err == nil || !errors.Is(err, mutationreceipt.ErrInvalidSnapshot) {
					t.Fatalf("conflicting duplicate restart = %p, %v", restarted, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			snapshot, err := restarted.receipt.store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Receipts) != 2 {
				t.Fatalf("exact duplicate charged twice: %+v", snapshot.Receipts)
			}
		})
	}
}

func TestReceiptBaselineSuffixChargesRetainedBaselineBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	baselineCost := receiptWALDecisionCost(mutationreceipt.Receipt{
		Intent: mutationreceipt.Intent{},
		Result: []byte("baseline-result"),
	})
	suffixCost := receiptWALDecisionCost(mutationreceipt.Receipt{
		Intent: mutationreceipt.Intent{},
		Result: []byte{0},
	})
	config.Receipt.MaxEntries = 2
	config.Receipt.MaxBytes = baselineCost + suffixCost - 1
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	suffix := receiptBaselineSuffixEnvelope(
		t, config.Receipt, 0x91, 1, image.cutoff.WallNs+int64(time.Millisecond),
		graphcache.EdgeKey[string]{Tail: "byte", Head: "limited"}, nil,
	)
	if _, err := runtime.log.Append(suffix, suffix.HLC); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if restarted != nil {
		_ = restarted.Close()
	}
	if !errors.Is(err, mutationreceipt.ErrCapacity) {
		t.Fatalf("byte-limited baseline suffix restart = %p, %v", restarted, err)
	}
}

func TestReceiptBaselineSuffixRejectsEpochAndPolicyMismatch(t *testing.T) {
	for _, mismatch := range []string{"epoch", "policy"} {
		t.Run(mismatch, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.wal")
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewLanternService(nil)
			if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
				t.Fatal(err)
			}
			envelopeConfig := config.Receipt
			if mismatch == "epoch" {
				envelopeConfig.Epoch = mutationreceipt.Epoch{0xee}
			}
			envelope := receiptBaselineSuffixEnvelope(
				t, envelopeConfig, 0x91, 1, image.cutoff.WallNs+int64(time.Millisecond),
				graphcache.EdgeKey[string]{Tail: "mismatch", Head: mismatch}, nil,
			)
			if mismatch == "policy" {
				envelope.PolicyFingerprint[0] ^= 1
			}
			if _, err := runtime.log.Append(envelope, envelope.HLC); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			if restarted, err := OpenDurableReceiptWALServingRuntime(config); restarted != nil || err == nil {
				if restarted != nil {
					_ = restarted.Close()
				}
				t.Fatalf("%s-mismatched suffix restarted: %p, %v", mismatch, restarted, err)
			}
		})
	}
}

func TestReceiptBaselineSuffixReplaysReplicatedDeletePastLocalCausalLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	config.ConfigureGraph = func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		graph.EnablePrefixIndex(func(key string) string { return key })
		graph.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{
			MaxVertexEntries: 16,
			MaxEdgeEntries:   1,
		})
		return nil
	}
	image := newReceiptBaselineTestImage(t, config)
	build := image.codec.build
	image.codec.build = func() (*ReceiptBaselineCandidate, error) {
		candidate, err := build()
		if err != nil {
			return nil, err
		}
		candidate.Graph.ApplySnapshotEdgeTombstoneHLC(
			"existing", "floor",
			hlc.Timestamp{WallNs: image.cutoff.WallNs - 1, NodeID: hlc.NodeID{0x90}},
			time.Now().Add(time.Hour),
		)
		return candidate, nil
	}
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	envelope := receiptBaselineSuffixEnvelope(
		t, config.Receipt, 0x91, 1, image.cutoff.WallNs+int64(time.Millisecond),
		graphcache.EdgeKey[string]{Tail: "over", Head: "limit"}, nil,
	)
	if _, err := runtime.log.Append(envelope, envelope.HLC); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	stats := restarted.graph.CausalMetadataStats()
	if stats.EdgeEntries != 2 || !stats.EdgeOverLimit {
		t.Fatalf("replicated suffix causal state = %+v", stats)
	}
}

func TestReceiptBaselineSuffixReplaysVertexReceiptFamiliesWithRetiredState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	highWater := image.capture.Receipts.ClockHighWaterMillis
	retiredState, retiredID := mustRetiredCatalogSnapshot(
		t, config.Receipt, highWater, 0x94,
	)
	setReceiptBaselineTestRetired(&image, retiredState)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	put := receiptBaselineVertexPutSuffixEnvelope(
		t, config.Receipt, 0x93, 1, image.cutoff.WallNs+int64(time.Millisecond),
	)
	deleteEnvelope := receiptBaselineVertexDeleteSuffixEnvelope(
		t, config.Receipt, 0x93, 2, put.HLC.WallNs+int64(time.Millisecond),
	)
	if _, err := runtime.log.Append(put, put.HLC); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.log.Append(deleteEnvelope, deleteEnvelope.HLC); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, live := restarted.graph.GetVertex("suffix-live"); live {
		t.Fatal("baseline suffix recovery resurrected exactly deleted Vertex")
	}
	if _, live := restarted.graph.GetVertex("suffix-expired"); live {
		t.Fatal("baseline suffix recovery resurrected expired Vertex Put")
	}
	if _, live := restarted.graph.GetVertex("suffix-origin-only"); live {
		t.Fatal("baseline suffix recovery applied an omitted receiver-local Vertex Put")
	}
	replication := restarted.graph.SnapshotReplication()
	if len(replication.Barriers.Vertices) != 1 ||
		replication.Barriers.Vertices[0].Key != "suffix-expired" {
		t.Fatalf("baseline suffix Vertex barriers = %+v", replication.Barriers.Vertices)
	}
	tombstones := make(map[string]bool)
	for _, tombstone := range replication.Tombstones.Vertices {
		tombstones[tombstone.Key] = true
	}
	if !tombstones["suffix-live"] || !tombstones["suffix-absent"] {
		t.Fatalf("baseline suffix Vertex tombstones = %+v", replication.Tombstones.Vertices)
	}
	snapshot, err := restarted.receipt.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Receipts) != 6 {
		t.Fatalf("baseline plus suffix receipt count = %d, want 6", len(snapshot.Receipts))
	}
	results := make(map[mutationreceipt.ID]mutationreceipt.Receipt, len(snapshot.Receipts))
	for _, receipt := range snapshot.Receipts {
		results[receipt.ID] = receipt
	}
	for _, receipt := range append(
		append([]mutationreceipt.Receipt{}, put.Receipts...),
		deleteEnvelope.Receipts...,
	) {
		got, ok := results[receipt.ID]
		if !ok || got.Kind != receipt.Kind ||
			!reflect.DeepEqual(got.Result, receipt.Result) ||
			got.Group != receipt.Group || got.Index != receipt.Index {
			t.Fatalf("baseline suffix receipt = %+v, %v; want %+v", got, ok, receipt)
		}
	}
	retiredSnapshot, _, err := restarted.receipt.retired.snapshot(
		restarted.receipt.policy,
		snapshot.ClockHighWaterMillis,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retiredSnapshot.ClockHighWaterMillis != snapshot.ClockHighWaterMillis {
		t.Fatalf(
			"retired high-water = %d, want active high-water %d",
			retiredSnapshot.ClockHighWaterMillis,
			snapshot.ClockHighWaterMillis,
		)
	}
	retiredConfig, _, err := retiredCatalogConfig(
		restarted.receipt.policy,
		snapshot.ClockHighWaterMillis,
	)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := mutationreceipt.NewRetiredCatalogFromSnapshot(retiredConfig, retiredSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	status, receipt, err := retired.Lookup(
		retiredID,
		time.UnixMilli(snapshot.ClockHighWaterMillis),
	)
	if err != nil || status != mutationreceipt.Confirmed ||
		!reflect.DeepEqual(receipt.Result, []byte{0x94, 4}) {
		t.Fatalf("retired receipt after suffix restart = %v, %+v, %v", status, receipt, err)
	}
	var foundOrigin bool
	for _, state := range restarted.origins.States() {
		if state.Origin == put.Origin {
			foundOrigin = state.LastSeq == 2 && state.LastHLC.Equal(deleteEnvelope.HLC)
		}
	}
	if !foundOrigin {
		t.Fatalf("baseline suffix origin frontier = %+v", restarted.origins.States())
	}
}

func TestReceiptBaselineSuffixDoesNotReapplyOmittedVertexDeleteAfterBlockerExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	now := time.Now()
	config.Receipt.ClockHighWater = now.Add(-3 * time.Hour).Truncate(time.Millisecond)
	config.Receipt.Retention = 24 * time.Hour
	config.Now = config.Receipt.ClockHighWater
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}

	oldWall := image.cutoff.WallNs + int64(time.Millisecond)
	blocker := recoveryGraphDeleteEffectEntry(
		t,
		0xd3,
		1,
		oldWall+int64(time.Millisecond),
		&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
			DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"blocked"}},
		}},
		now.Add(-time.Hour),
		0,
	)
	omitted := receiptVertexDeleteRecoveryEnvelope(
		t,
		config.Receipt,
		0xd4,
		1,
		oldWall,
		now.Add(time.Hour),
		[]string{"blocked"},
	)
	if _, err := runtime.log.Append(blocker.Op, blocker.HLC); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.log.Append(omitted, omitted.HLC); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for _, tombstone := range restarted.graph.SnapshotReplication().Tombstones.Vertices {
		if tombstone.Key == "blocked" {
			t.Fatalf("baseline suffix reapplied omitted older Vertex Delete: %+v", tombstone)
		}
	}
	snapshot, err := restarted.receipt.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, receipt := range snapshot.Receipts {
		found = found || receipt.ID == omitted.Receipts[0].ID
	}
	if !found {
		t.Fatal("baseline suffix lost omitted Vertex Delete receipt")
	}
}

func receiptBaselineVertexPutSuffixEnvelope(
	t *testing.T,
	config mutationreceipt.Config,
	originByte byte,
	originSequence uint64,
	wallNS int64,
) *vertexPutReceiptEnvelope {
	t.Helper()
	issued := time.UnixMilli(wallNS / int64(time.Millisecond)).UTC()
	var origin hlc.NodeID
	for i := range origin {
		origin[i] = originByte
	}
	stamp := hlc.Timestamp{WallNs: issued.UnixNano(), NodeID: origin}
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	original := []*pb.Vertex{
		{
			Key: "suffix-live", Value: &pb.Vertex_String_{String_: "value"},
			Expiration: timestamppb.New(issued.Add(time.Hour)),
		},
		{
			Key: "suffix-expired", Value: &pb.Vertex_String_{String_: "expired"},
			Expiration: timestamppb.New(time.Now().Add(-time.Hour)),
		},
		{
			Key: "suffix-origin-only", Value: &pb.Vertex_String_{String_: "not accepted locally"},
			Expiration: timestamppb.New(issued.Add(time.Hour)),
		},
	}
	outcomes := []pb.PutOutcome{
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_EXPIRED,
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
	}
	group := mutationreceipt.GroupID{0xb1}
	receipts := make([]mutationreceipt.Receipt, len(original))
	for i, vertex := range original {
		id, err := mutationreceipt.NewID(config.Epoch, issued, [24]byte{originByte, byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		digest, err := vertexPutDigest(vertex, false)
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: group, Index: uint32(i), Count: uint32(len(original)),
				Kind: mutationreceipt.PutVertex, Digest: digest,
			},
			Result: []byte{byte(outcomes[i])}, DeadlineMillis: issued.Add(config.Retention).UnixMilli(),
		}
	}
	envelope := &vertexPutReceiptEnvelope{
		Origin: origin, OriginSeq: originSequence, HLC: stamp,
		Epoch: config.Epoch, PolicyFingerprint: store.PolicyFingerprint(),
		Original: original,
		Accepted: []graphcache.IndexedVertexPut[string, *pb.Vertex]{
			{
				Index: 0, Outcome: graphcache.PutOutcomeAppliedAndLive,
				Item: graphcache.VertexItem[string, *pb.Vertex]{
					Key: "suffix-live", Value: proto.Clone(original[0]).(*pb.Vertex),
					Expiration: original[0].GetExpiration().AsTime(),
				},
			},
			{
				Index: 1, Outcome: graphcache.PutOutcomeExpired,
				Item: graphcache.VertexItem[string, *pb.Vertex]{
					Key: "suffix-expired", CausalBarrier: true,
				},
			},
		},
		Receipts: receipts,
	}
	envelope.Mutation = receiptVertexPutGraphMutation(envelope)
	if err := validateReceiptVertexPutWALEntry(mutationlog.Entry{
		Seq: 1, HLC: stamp, Op: envelope,
	}); err != nil {
		t.Fatalf("invalid Vertex Put suffix fixture: %v", err)
	}
	return envelope
}

func receiptBaselineVertexDeleteSuffixEnvelope(
	t *testing.T,
	config mutationreceipt.Config,
	originByte byte,
	originSequence uint64,
	wallNS int64,
) *vertexDeleteReceiptEnvelope {
	t.Helper()
	issued := time.UnixMilli(wallNS / int64(time.Millisecond)).UTC()
	var origin hlc.NodeID
	for i := range origin {
		origin[i] = originByte
	}
	stamp := hlc.Timestamp{WallNs: issued.UnixNano(), NodeID: origin}
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"suffix-live", "suffix-absent"}
	results := []byte{1, 0}
	group := mutationreceipt.GroupID{0xb2}
	receipts := make([]mutationreceipt.Receipt, len(keys))
	for i, key := range keys {
		id, err := mutationreceipt.NewID(config.Epoch, issued, [24]byte{originByte, byte(i + 3)})
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: group, Index: uint32(i), Count: uint32(len(keys)),
				Kind: mutationreceipt.DeleteVertex, Digest: vertexDeleteDigest(key),
			},
			Result: []byte{results[i]}, DeadlineMillis: issued.Add(config.Retention).UnixMilli(),
		}
	}
	envelope := &vertexDeleteReceiptEnvelope{
		Origin: origin, OriginSeq: originSequence, HLC: stamp,
		Epoch: config.Epoch, PolicyFingerprint: store.PolicyFingerprint(),
		TombstoneExpiration: issued.Add(time.Hour),
		OriginalKeys:        keys,
		Accepted: []graphcache.IndexedVertexDelete[string]{
			{Index: 0, Key: keys[0]}, {Index: 1, Key: keys[1]},
		},
		Receipts: receipts,
	}
	envelope.Mutation = receiptVertexDeleteGraphMutation(envelope)
	if err := validateReceiptVertexDeleteWALEntry(mutationlog.Entry{
		Seq: 1, HLC: stamp, Op: envelope,
	}); err != nil {
		t.Fatalf("invalid Vertex Delete suffix fixture: %v", err)
	}
	return envelope
}

func receiptBaselineSuffixEnvelope(
	t *testing.T,
	config mutationreceipt.Config,
	originByte byte,
	originSequence uint64,
	wallNS int64,
	key graphcache.EdgeKey[string],
	duplicate *mutationreceipt.Receipt,
) *edgeDeleteReceiptEnvelope {
	t.Helper()
	issued := time.UnixMilli(wallNS / int64(time.Millisecond)).UTC()
	var origin hlc.NodeID
	for i := range origin {
		origin[i] = originByte
	}
	stamp := hlc.Timestamp{
		WallNs: issued.UnixNano(),
		NodeID: origin,
	}
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	var receipt mutationreceipt.Receipt
	if duplicate == nil {
		id, err := mutationreceipt.NewID(config.Epoch, issued, [24]byte{originByte})
		if err != nil {
			t.Fatal(err)
		}
		receipt = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: mutationreceipt.GroupID{0xa1}, Count: 1,
				Kind: mutationreceipt.DeleteEdge, Digest: edgeDeleteDigest(key.Tail, key.Head),
			},
			DeadlineMillis: issued.Add(config.Retention).UnixMilli(),
			Result:         []byte{1},
		}
	} else {
		receipt = *duplicate
		receipt.Result = append([]byte(nil), duplicate.Result...)
		receipt.Digest = edgeDeleteDigest(key.Tail, key.Head)
	}
	envelope := &edgeDeleteReceiptEnvelope{
		Origin:              stamp.NodeID,
		OriginSeq:           originSequence,
		HLC:                 stamp,
		Epoch:               config.Epoch,
		PolicyFingerprint:   store.PolicyFingerprint(),
		TombstoneExpiration: issued.Add(time.Hour),
		OriginalKeys:        []graphcache.EdgeKey[string]{key},
		Accepted:            []graphcache.IndexedEdgeDelete[string]{{Index: 0, Key: key}},
		Receipts:            []mutationreceipt.Receipt{receipt},
	}
	envelope.Mutation = receiptEdgeDeleteWALMutation(envelope)
	if err := validateReceiptEdgeDeleteWALEntry(mutationlog.Entry{Seq: 1, HLC: stamp, Op: envelope}); err != nil {
		t.Fatalf("invalid suffix fixture: %v", err)
	}
	return envelope
}

func receiptBaselineTestFrameOffsets(t *testing.T, raw []byte) []int {
	t.Helper()
	const walMagicSize = 8
	const frameHeaderSize = 8
	var offsets []int
	for offset := walMagicSize; offset < len(raw); {
		if len(raw)-offset < frameHeaderSize {
			t.Fatal("truncated test WAL frame")
		}
		bodySize := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
		if bodySize <= 0 || bodySize > len(raw)-offset-frameHeaderSize {
			t.Fatal("invalid test WAL frame size")
		}
		offsets = append(offsets, offset)
		offset += frameHeaderSize + bodySize
	}
	return offsets
}
