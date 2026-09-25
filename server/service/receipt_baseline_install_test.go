package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	raw   []byte
	build func() (*ReceiptBaselineCandidate, error)
}

func (c *receiptBaselineTestCodec) EncodeCombinedReceiptBaseline(
	ctx context.Context,
	_ ReceiptWholeStateCapture,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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

func newReceiptBaselineTestImage(t *testing.T, config DurableReceiptWALRuntimeConfig) receiptBaselineTestImage {
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
			&pb.Vertex{Key: "baseline-searchable", Expiration: timestamppb.New(expiration)},
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

func baselineRuntimeTestConfig(path string) DurableReceiptWALRuntimeConfig {
	now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	return DurableReceiptWALRuntimeConfig{
		Path: path,
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

func assertReceiptBaselinePublicationFault(t *testing.T, service *LanternService) {
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
	if _, err := (&lanternServiceConnect{svc: service}).GetVertex(
		context.Background(),
		connect.NewRequest(&pb.GetVertexRequest{Key: "baseline-searchable"}),
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("external graph read after receipt baseline failure = %v", err)
	}
}

func receiptBaselineSidecars(t *testing.T, path string) []string {
	t.Helper()
	matches, err := filepath.Glob(path + ".receipt.*.baseline")
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestInstallReceiptBaselineRestartPreservesWholeStateAndSuffix(t *testing.T) {
	path := t.TempDir() + "/receipts.wal"
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	highWater := image.capture.Receipts.ClockHighWaterMillis
	retiredState, _ := mustRetiredCatalogSnapshot(
		t,
		config.Receipt,
		highWater,
		0x47,
	)
	setReceiptBaselineTestRetired(&image, retiredState)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	graphIdentity, storeIdentity := runtime.graph, runtime.receipt.store
	originsIdentity, logIdentity, clockIdentity := runtime.origins, runtime.log, runtime.clock
	genesisGeneration := runtime.receipt.generation
	oldSubscriber, cancel, err := runtime.log.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	laterHighWater := image.capture.Policy.ClockHighWater.Add(2 * time.Minute)

	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
		t.Fatal(err)
	}
	if runtime.graph != graphIdentity || runtime.receipt.store != storeIdentity ||
		runtime.origins != originsIdentity || runtime.log != logIdentity || runtime.clock != clockIdentity {
		t.Fatal("baseline install replaced a runtime-owned object")
	}
	if runtime.receipt.generation == genesisGeneration || runtime.receipt.generation == ([16]byte{}) {
		t.Fatal("baseline install did not rotate the active generation")
	}
	rotatedGeneration := runtime.receipt.generation
	select {
	case _, open := <-oldSubscriber:
		if open {
			t.Fatal("old subscriber received the private marker")
		}
	case <-time.After(time.Second):
		t.Fatal("old subscriber was not gapped")
	}
	if _, ok := runtime.graph.GetVertex("baseline-searchable"); !ok {
		t.Fatal("baseline graph was not published")
	}
	if hits := runtime.graph.SearchVertices("baseline", 10, ""); len(hits) == 0 || hits[0].ID != "baseline-searchable" {
		t.Fatalf("baseline search index = %+v", hits)
	}
	if status, receipt, err := runtime.receipt.store.Lookup(image.id, laterHighWater); err != nil ||
		status != mutationreceipt.Confirmed || string(receipt.Result) != "baseline-result" {
		t.Fatalf("baseline receipt = %v, %+v, %v", status, receipt, err)
	}
	if got := runtime.receipt.store.Stats().HighWaterMillis; got != laterHighWater.UnixMilli() {
		t.Fatalf("Store high-water = %d, want %d", got, laterHighWater.UnixMilli())
	}
	if states := runtime.origins.States(); len(states) != 1 || states[0].LastSeq != 1 {
		t.Fatalf("baseline origins = %+v", states)
	}
	if length, _, evicted := runtime.MutationLogStats(); length != 0 || evicted != 1 {
		t.Fatalf("marker boundary log = len %d evicted %d", length, evicted)
	}
	capability, err := primary.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || capability.GetEnabled() {
		t.Fatalf("baseline install exposed receipt capability: %+v, %v", capability, err)
	}
	if _, err := primary.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: image.id.Bytes(),
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("baseline install exposed receipt status: %v", err)
	}

	suffix := recoveryGraphPutEffectEntry(t, 0x88, image.cutoff.WallNs+1, &pb.MutationOp{
		Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "suffix-only", Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		}},
	})
	suffix.Op.(*graphPutEffectEnvelope).Mutation.Seq = 2
	if appended, err := runtime.log.Append(suffix.Op, suffix.HLC); err != nil || appended.Seq != 2 {
		t.Fatalf("append suffix = %+v, %v", appended, err)
	}
	if _, ok := runtime.graph.GetVertex("suffix-only"); ok {
		t.Fatal("WAL-only suffix was prematurely published")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	config.Now = laterHighWater
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if restarted.receipt.generation != rotatedGeneration {
		t.Fatalf("active generation = %x, want %x", restarted.receipt.generation, rotatedGeneration)
	}
	if _, ok := restarted.graph.GetVertex("baseline-searchable"); !ok {
		t.Fatal("restart lost baseline graph")
	}
	if _, ok := restarted.graph.GetVertex("suffix-only"); !ok {
		t.Fatal("restart did not replay WAL suffix")
	}
	if status, receipt, err := restarted.receipt.store.Lookup(image.id, laterHighWater); err != nil ||
		status != mutationreceipt.Confirmed || string(receipt.Result) != "baseline-result" {
		t.Fatalf("restart receipt = %v, %+v, %v", status, receipt, err)
	}
	if states := restarted.origins.States(); len(states) != 1 || states[0].LastSeq != 2 ||
		!states[0].LastHLC.Equal(suffix.HLC) {
		t.Fatalf("restart origins = %+v", states)
	}
	if last, ok := restarted.log.LastSeq(); !ok || last != 2 {
		t.Fatalf("restart Log LastSeq = %d, %t", last, ok)
	}
	if first, ok := restarted.log.FirstSeq(); !ok || first != 2 {
		t.Fatalf("restart Log FirstSeq = %d, %t", first, ok)
	}
	if _, _, err := restarted.log.Subscribe(1); !errors.Is(err, mutationlog.ErrGapped) {
		t.Fatalf("old cursor after restart = %v", err)
	}
	if next := restarted.clock.Now(); !suffix.HLC.Less(next) {
		t.Fatalf("restart HLC = %+v, want above suffix %+v", next, suffix.HLC)
	}
}

func TestInstallReceiptBaselineRetiredStateInvariants(t *testing.T) {
	t.Run("unions retired catalog without replacing slot", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "receipts.wal")
		config := baselineRuntimeTestConfig(path)
		image := newReceiptBaselineTestImage(t, config)
		highWater := image.capture.Receipts.ClockHighWaterMillis
		incoming, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x41)
		local, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x42)
		setReceiptBaselineTestRetired(&image, incoming)
		config.BaselineCodec = image.codec
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if err != nil {
			t.Fatal(err)
		}

		t.Run("retries when retired catalog advances during encoding", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.wal")
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			highWater := image.capture.Receipts.ClockHighWaterMillis
			incoming, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x43)
			local, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x44)
			setReceiptBaselineTestRetired(&image, incoming)
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			primary := runtime.NewLanternService(nil)
			hookCalls := 0
			runtime.receipt.installFault = func(point receiptBaselineInstallFaultPoint) error {
				if point == receiptBaselineAfterSidecarBeforeFinalCut && hookCalls == 0 {
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
			if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 {
				t.Fatalf("retry sidecars = %v, want one committed sidecar", sidecars)
			}
		})
		t.Cleanup(func() { _ = runtime.Close() })
		slot := runtime.receipt.retired
		mustReplaceRetiredCatalog(t, slot, runtime.receipt.policy, highWater, local)
		primary := runtime.NewLanternService(nil)

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
				path := filepath.Join(t.TempDir(), "receipts.wal")
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
				runtime, err := CreateDurableReceiptWALServingRuntime(config)
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
				primary := runtime.NewLanternService(nil)
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
		path := filepath.Join(t.TempDir(), "receipts.wal")
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
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if err != nil {
			t.Fatal(err)
		}
		primary := runtime.NewLanternService(nil)
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

		scan, err := scanReceiptBaselineWAL(path, config.Receipt, config.NodeID)
		if err != nil {
			t.Fatal(err)
		}
		if !scan.hasMarker || scan.markerSequence != 1 ||
			scan.marker.ReceiptHighWaterMillis != effectiveHighWater {
			t.Fatalf("newest baseline marker = %+v", scan)
		}

		config.BaselineCodec = firstCodec
		config.Now = time.UnixMilli(initialHighWater)
		config.Receipt.ClockHighWater = time.UnixMilli(initialHighWater)
		restarted, err := OpenDurableReceiptWALServingRuntime(config)
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
	for _, point := range []receiptBaselineSidecarFaultPoint{
		receiptBaselineBeforeRename,
		receiptBaselineAfterRename,
	} {
		t.Run(string(point), func(t *testing.T) {
			path := t.TempDir() + "/receipts.wal"
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewLanternService(nil)
			runtime.receipt.sidecarFault = func(got receiptBaselineSidecarFaultPoint) error {
				if got == point {
					return errors.New("injected sidecar crash")
				}
				return nil
			}
			if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err == nil {
				t.Fatal("sidecar crash point succeeded")
			}
			if _, ok := runtime.log.LastSeq(); ok {
				t.Fatal("sidecar crash committed a marker")
			}
			if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
				t.Fatal("sidecar crash published baseline graph")
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			runtime, err = OpenDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
				t.Fatal("orphan sidecar became a restart baseline")
			}
			if matches := receiptBaselineSidecars(t, path); len(matches) != 0 {
				t.Fatalf("orphan cleanup = %v", matches)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, point := range []receiptBaselineInstallFaultPoint{
		receiptBaselineAfterMarkerBeforePublish,
		receiptBaselineAfterPublish,
	} {
		t.Run(string(point), func(t *testing.T) {
			path := t.TempDir() + "/receipts.wal"
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			highWater := image.capture.Receipts.ClockHighWaterMillis
			retiredState, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x71)
			setReceiptBaselineTestRetired(&image, retiredState)
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewLanternService(nil)
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
			restarted, err := OpenDurableReceiptWALServingRuntime(config)
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
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil)
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
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil)
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
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil)
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
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
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

	scan, err := scanReceiptBaselineWAL(path, config.Receipt, config.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !scan.hasMarker || scan.markerSequence != 2 || scan.firstGeneration != genesis ||
		scan.activeGeneration != active || scan.marker.PreviousGeneration == genesis {
		t.Fatalf("concurrent marker chain = %+v, genesis %x active %x", scan, genesis, active)
	}
	if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 {
		t.Fatalf("concurrent install sidecars = %v, want one committed candidate", sidecars)
	}
}

func TestInstallReceiptBaselineWALFailureRollsBackStore(t *testing.T) {
	for _, tc := range []struct {
		name        string
		walError    func(error) error
		wantFaulted bool
	}{
		{
			name: "definite abort",
			walError: func(cause error) error {
				return &mutationlog.DefiniteWALAbort{Cause: cause}
			},
		},
		{
			name:        "indeterminate",
			walError:    func(cause error) error { return cause },
			wantFaulted: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.wal")
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			newHighWater := image.capture.Receipts.ClockHighWaterMillis + int64(5*time.Minute/time.Millisecond)
			image.capture.Receipts.ClockHighWaterMillis = newHighWater
			image.capture.Policy.ClockHighWater = time.UnixMilli(newHighWater)
			retired, err := newEmptyRetiredCatalogSnapshot(
				image.capture.Policy,
				newHighWater,
			)
			if err != nil {
				t.Fatal(err)
			}
			image.capture.Retired = retired
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
				state.ClockHighWaterMillis = newHighWater
				policy := candidate.Policy
				policy.ClockHighWater = time.UnixMilli(newHighWater)
				candidate.Receipts, err = mutationreceipt.NewFromSnapshot(policy, state)
				if err != nil {
					return nil, err
				}
				candidate.Retired, err = newEmptyRetiredCatalogSnapshot(policy, newHighWater)
				candidate.Policy = policy
				return candidate, err
			}
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			originalLog := runtime.log
			cause := errors.New("injected marker write failure")
			failingLog := mutationlog.New(mutationlog.Options{
				Capacity: 8,
				WAL: receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
					return tc.walError(cause)
				}),
			})
			runtime.log = failingLog
			primary := runtime.NewLanternService(nil)
			originalHighWater := runtime.receipt.store.Stats().HighWaterMillis
			generation := runtime.receipt.generation

			err = primary.InstallReceiptBaseline(context.Background(), image.capture)
			if err == nil || !errors.Is(err, cause) {
				t.Fatalf("marker failure = %v", err)
			}
			if primary.receiptCommitFaulted != tc.wantFaulted {
				t.Fatalf("receipt fail-stop = %t, want %t", primary.receiptCommitFaulted, tc.wantFaulted)
			}
			if tc.wantFaulted {
				assertReceiptBaselinePublicationFault(t, primary)
			} else {
				generation, faulted := primary.publicationStatus()
				if faulted {
					t.Fatal("definite marker abort faulted publication")
				}
				select {
				case <-generation:
					t.Fatal("definite marker abort closed publication generation")
				default:
				}
			}
			if got := runtime.receipt.store.Stats(); got.HighWaterMillis != originalHighWater || got.Entries != 0 {
				t.Fatalf("failed marker changed Store = %+v, want high-water %d and no rows", got, originalHighWater)
			}
			if runtime.receipt.generation != generation {
				t.Fatal("failed marker rotated generation")
			}
			if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
				t.Fatal("failed marker published graph")
			}
			sidecars := receiptBaselineSidecars(t, path)
			if tc.wantFaulted && len(sidecars) != 1 {
				t.Fatalf("indeterminate marker sidecars = %v, want retained candidate", sidecars)
			}
			if !tc.wantFaulted && len(sidecars) != 0 {
				t.Fatalf("definite marker abort sidecars = %v, want no candidate", sidecars)
			}
			if tc.wantFaulted {
				retained := sidecars[0]
				image.codec.raw = []byte("candidate-after-indeterminate-marker")
				if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err == nil {
					t.Fatal("fail-stopped service accepted another baseline")
				}
				if after := receiptBaselineSidecars(t, path); len(after) != 1 || after[0] != retained {
					t.Fatalf("later rejection removed uncertain candidate: before %v after %v", sidecars, after)
				}
			}

			runtime.log = originalLog
			if err := failingLog.Close(); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallReceiptBaselineClosedLogCleansRejectedCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil)

	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
		t.Fatal(err)
	}
	committedDigest := runtime.receipt.committedBaseline
	committedGeneration := runtime.receipt.generation
	committedSidecars := receiptBaselineSidecars(t, path)
	if len(committedSidecars) != 1 {
		t.Fatalf("committed sidecars = %v, want one", committedSidecars)
	}
	if err := runtime.log.Close(); err != nil {
		t.Fatal(err)
	}

	image.codec.raw = []byte("candidate-rejected-by-closed-log")
	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); !errors.Is(err, mutationlog.ErrClosed) {
		t.Fatalf("closed-log baseline error = %v, want ErrClosed", err)
	}
	if primary.receiptCommitFaulted {
		t.Fatal("definite closed-log rejection faulted receipt publication")
	}
	if runtime.receipt.committedBaseline != committedDigest {
		t.Fatal("closed-log rejection changed committed digest")
	}
	if runtime.receipt.generation != committedGeneration {
		t.Fatal("closed-log rejection rotated generation")
	}
	if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 ||
		sidecars[0] != committedSidecars[0] {
		t.Fatalf("closed-log rejection sidecars = %v, want %v", sidecars, committedSidecars)
	}
}

func TestInstallReceiptBaselineLegacyWALUncertaintyCleansRejectedCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil)

	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
		t.Fatal(err)
	}
	committedDigest := runtime.receipt.committedBaseline
	committedGeneration := runtime.receipt.generation
	committedSidecars := receiptBaselineSidecars(t, path)
	if len(committedSidecars) != 1 {
		t.Fatalf("committed sidecars = %v, want one", committedSidecars)
	}

	originalLog := runtime.log
	uncertainLog := mutationlog.New(mutationlog.Options{
		Capacity: 8,
		WAL: receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
			return errors.New("injected legacy WAL uncertainty")
		}),
	})
	runtime.log = uncertainLog
	defer func() {
		runtime.log = originalLog
		_ = uncertainLog.Close()
	}()
	if _, err := uncertainLog.Append("legacy", image.cutoff); err == nil {
		t.Fatal("legacy append unexpectedly succeeded")
	}
	primary = runtime.NewLanternService(nil)

	image.codec.raw = []byte("candidate-rejected-by-legacy-uncertainty")
	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); !errors.Is(err, mutationlog.ErrLegacyWALUncertain) {
		t.Fatalf("legacy-uncertain baseline error = %v, want ErrLegacyWALUncertain", err)
	}
	assertReceiptBaselinePublicationFault(t, primary)
	if runtime.receipt.committedBaseline != committedDigest {
		t.Fatal("legacy-uncertain rejection changed committed digest")
	}
	if runtime.receipt.generation != committedGeneration {
		t.Fatal("legacy-uncertain rejection rotated generation")
	}
	if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 ||
		sidecars[0] != committedSidecars[0] {
		t.Fatalf("legacy-uncertain rejection sidecars = %v, want %v", sidecars, committedSidecars)
	}
}

func TestInstallReceiptBaselineBoundsLiveSidecarRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil)

	for i := range 12 {
		image.codec.raw = []byte(fmt.Sprintf("canonical-success-%02d", i))
		if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
			t.Fatalf("successful install %d: %v", i, err)
		}
		if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 {
			t.Fatalf("successful install %d sidecars = %v, want one", i, sidecars)
		}
	}
	committedDigest := runtime.receipt.committedBaseline
	newer := image.cutoff
	newer.WallNs++
	if !runtime.origins.Record(image.origin, 2, newer) {
		t.Fatal("failed to seed newer receiver origin")
	}
	for i := range 12 {
		image.codec.raw = []byte(fmt.Sprintf("canonical-rejected-%02d", i))
		if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err == nil {
			t.Fatalf("origin-regressing install %d succeeded", i)
		}
		if runtime.receipt.committedBaseline != committedDigest {
			t.Fatalf("rejected install %d changed committed digest", i)
		}
		if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 {
			t.Fatalf("rejected install %d sidecars = %v, want prior committed candidate", i, sidecars)
		}
	}
}

func TestInstallReceiptBaselineRestartRetainsCommittedSidecarOnRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
		t.Fatal(err)
	}
	committedSidecars := receiptBaselineSidecars(t, path)
	if len(committedSidecars) != 1 {
		t.Fatalf("committed sidecars = %v, want one", committedSidecars)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary := restarted.NewLanternService(nil)
	newer := image.cutoff
	newer.WallNs++
	if !restarted.origins.Record(image.origin, 2, newer) {
		t.Fatal("failed to seed newer receiver origin")
	}
	image.codec.raw = []byte("candidate-rejected-after-restart")
	if err := restartedPrimary.InstallReceiptBaseline(context.Background(), image.capture); err == nil {
		t.Fatal("origin-regressing baseline installed after restart")
	}
	if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 ||
		sidecars[0] != committedSidecars[0] {
		t.Fatalf("restart rejection sidecars = %v, want %v", sidecars, committedSidecars)
	}
}

func TestInstallReceiptBaselineCleanupFailureReportsCommittedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	var logs bytes.Buffer
	primary := runtime.NewLanternService(nil).WithLogger(
		slog.New(slog.NewTextHandler(&logs, nil)),
	)
	runtime.receipt.sidecarFault = func(point receiptBaselineSidecarFaultPoint) error {
		if point == receiptBaselineBeforeCleanup {
			return errors.New("injected cleanup failure")
		}
		return nil
	}

	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
		t.Fatalf("committed install reported cleanup failure: %v", err)
	}
	if _, ok := runtime.graph.GetVertex("baseline-searchable"); !ok {
		t.Fatal("cleanup failure hid committed graph")
	}
	if !strings.Contains(logs.String(), "receipt baseline committed but stale sidecar cleanup failed") ||
		!strings.Contains(logs.String(), "injected cleanup failure") {
		t.Fatalf("cleanup failure log = %q", logs.String())
	}
	if sidecars := receiptBaselineSidecars(t, path); len(sidecars) != 1 {
		t.Fatalf("cleanup failure sidecars = %v, want committed candidate", sidecars)
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
	path := t.TempDir() + "/receipts.wal"
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	blocking := &blockingReceiptBaselineCodec{
		delegate: image.codec,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	config.BaselineCodec = blocking
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewLanternReplicationService(primary)
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

func TestInstallReceiptBaselineRestartPreservesEffectiveReceiptHighWater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
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
			candidate.Policy = policy
			return candidate, err
		}
	}

	firstCapture := image.capture
	firstCapture.Receipts.ClockHighWaterMillis = effectiveHighWater
	firstCapture.Receipts.Receipts = nil
	firstCapture.Policy.ClockHighWater = time.UnixMilli(effectiveHighWater)
	firstCodec := &receiptBaselineTestCodec{
		raw:   []byte("canonical-high-water-baseline-a"),
		build: buildAtHighWater(effectiveHighWater, false),
	}
	secondCapture := image.capture
	secondCapture.Receipts.ClockHighWaterMillis = sourceHighWater
	secondCapture.Policy.ClockHighWater = time.UnixMilli(sourceHighWater)
	secondCodec := &receiptBaselineTestCodec{
		raw:   []byte("canonical-high-water-baseline-b"),
		build: buildAtHighWater(sourceHighWater, true),
	}

	config.BaselineCodec = firstCodec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(context.Background(), firstCapture); err != nil {
		t.Fatal(err)
	}
	if got := runtime.receipt.store.Stats(); got.HighWaterMillis != effectiveHighWater || got.Entries != 0 {
		t.Fatalf("first baseline Store = %+v", got)
	}

	runtime.receipt.baselineCodec = secondCodec
	if err := primary.InstallReceiptBaseline(context.Background(), secondCapture); err != nil {
		t.Fatal(err)
	}
	if got := runtime.receipt.store.Stats(); got.HighWaterMillis != effectiveHighWater || got.Entries != 0 {
		t.Fatalf("second baseline regressed live Store = %+v", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	scan, err := scanReceiptBaselineWAL(path, config.Receipt, config.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !scan.hasMarker || scan.markerSequence != 2 ||
		scan.marker.ReceiptHighWaterMillis != effectiveHighWater {
		t.Fatalf("newest baseline marker = %+v", scan)
	}

	config.BaselineCodec = secondCodec
	config.Now = time.UnixMilli(initialHighWater)
	config.Receipt.ClockHighWater = time.UnixMilli(initialHighWater)
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
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
}
