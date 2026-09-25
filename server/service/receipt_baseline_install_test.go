package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
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

func (c *receiptBaselineTestCodec) EncodeReceiptBaseline(
	ctx context.Context,
	_ ReceiptWholeStateCapture,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]byte(nil), c.raw...), nil
}

func (c *receiptBaselineTestCodec) StageReceiptBaseline(
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

func newReceiptBaselineTestImage(t *testing.T, config DurableReceiptWALRuntimeConfig) receiptBaselineTestImage {
	t.Helper()
	issued := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
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
			Graph: graph, Receipts: receipts, Policy: policy,
			Origins:        append([]OriginState(nil), origins...),
			CutoffLocalSeq: 41, CutoffHLC: cutoff,
		}, nil
	}
	return receiptBaselineTestImage{
		capture: ReceiptWholeStateCapture{Receipts: snapshot, Policy: policy, Origins: origins},
		codec:   &receiptBaselineTestCodec{raw: []byte("canonical-test-receipt-baseline-v1"), build: build},
		id:      id,
		origin:  origin,
		cutoff:  cutoff,
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

func TestInstallReceiptBaselineRestartPreservesWholeStateAndSuffix(t *testing.T) {
	path := t.TempDir() + "/receipts.wal"
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
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
	var absent mutationreceipt.ID
	absent[0] = 1
	_, _, _ = runtime.receipt.store.Lookup(absent, laterHighWater)

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
	if _, err := primary.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
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
			matches, err := filepath.Glob(path + ".receipt-v1.*.baseline")
			if err != nil || len(matches) != 0 {
				t.Fatalf("orphan cleanup = %v, %v", matches, err)
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
			} else if _, ok := runtime.graph.GetVertex("baseline-searchable"); !ok {
				t.Fatal("post-publication crash lost installed graph")
			}
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
			if got := runtime.receipt.store.Stats(); got.HighWaterMillis != originalHighWater || got.Entries != 0 {
				t.Fatalf("failed marker changed Store = %+v, want high-water %d and no rows", got, originalHighWater)
			}
			if runtime.receipt.generation != generation {
				t.Fatal("failed marker rotated generation")
			}
			if _, ok := runtime.graph.GetVertex("baseline-searchable"); ok {
				t.Fatal("failed marker published graph")
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
