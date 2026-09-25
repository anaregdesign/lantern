package backup

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

func newReceiptSnapshotInstallerFixture(
	t *testing.T,
	frames []*pb.SnapshotResponse,
	limits ReceiptSnapshotCollectorLimits,
) (*ReceiptSnapshotInstaller, *service.ServingRuntime) {
	t.Helper()
	policy := receiptSnapshotPolicyFromHeader(t, frames[0].GetHeader())
	dir := t.TempDir()
	configureGraph := func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		graph.EnablePrefixIndex(func(key string) string { return key })
		return nil
	}
	config := service.DurableReceiptWALRuntimeConfig{
		Path:           filepath.Join(dir, "receipts.wal"),
		Receipt:        policy,
		Log:            mutationlog.Options{Capacity: 8, SubscriberBuffer: 2},
		DefaultTTL:     time.Hour,
		ConfigureGraph: configureGraph,
		NodeID:         hlc.NodeID{0x71},
		Now:            policy.ClockHighWater,
		BaselineCodec:  ReceiptBaselineCodec{},
	}
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replicationService, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	collector, err := NewReceiptSnapshotCollector(ReceiptSnapshotCollectorConfig{
		TempDir: dir, Limits: limits, ExpectedPolicy: policy,
		ExpectedRetiredConfig: receiptSnapshotRetiredConfig(policy),
		DefaultTTL:            time.Hour,
		ConfigureGraph:        configureGraph,
	})
	if err != nil {
		t.Fatal(err)
	}
	installer, err := NewReceiptSnapshotInstaller(collector, primary, nil)
	if err != nil {
		t.Fatal(err)
	}
	return installer, runtime
}

func receiptSnapshotPolicyFromHeader(t *testing.T, header *pb.SnapshotHeader) mutationreceipt.Config {
	t.Helper()
	metadata := header.GetReceiptMetadata()
	wire := metadata.GetActivePolicy()
	var epoch mutationreceipt.Epoch
	copy(epoch[:], wire.GetDeploymentEpoch())
	return mutationreceipt.Config{
		Epoch:          epoch,
		Retention:      time.Duration(wire.GetRetentionMs()) * time.Millisecond,
		MaxEntries:     int(wire.GetMaxEntries()),
		MaxBytes:       wire.GetMaxBytes(),
		ClockHighWater: time.UnixMilli(int64(metadata.GetClockHighWaterUnixMs())),
	}
}

func TestReceiptSnapshotInstallerPublishesOnlyCompleteReceiptV2(t *testing.T) {
	frames, _ := receiptSnapshotCollectorFixture(t)
	installer, runtime := newReceiptSnapshotInstallerFixture(
		t, frames, receiptSnapshotCollectorLimits(),
	)
	if got := installer.RequiredFormat(); got != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT ||
		!installer.CompatibleFormat(got) ||
		installer.CompatibleFormat(pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1) {
		t.Fatalf("format policy = %v", got)
	}

	result, err := installer.Install(
		context.Background(),
		&receiptSnapshotTestStream{frames: frames, current: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT ||
		result.Graph.Vertices != 2 || result.Graph.Edges != 1 {
		t.Fatalf("install result = %+v", result)
	}
	if got, _, ok := runtime.GraphCache().GetEdgeDetail("tail", "head"); !ok || got != 1.5 {
		t.Fatalf("installed edge = %v, %t", got, ok)
	}
	if length, _, evicted := runtime.MutationLogStats(); length != 0 || evicted != 1 {
		t.Fatalf("post-install log = len %d evicted %d, want 0 and 1", length, evicted)
	}
}

func TestReceiptSnapshotInstallerPreservesTargetRetiredEvidence(t *testing.T) {
	frames, policy := receiptSnapshotCollectorFixture(t)
	installer, runtime := newReceiptSnapshotInstallerFixture(
		t,
		frames,
		receiptSnapshotCollectorLimits(),
	)
	capture, err := service.DecodeReceiptSnapshotFrames(
		frames,
		policy,
		receiptSnapshotRetiredConfig(policy),
	)
	if err != nil {
		t.Fatal(err)
	}
	capture.Retired = producerRetiredSnapshot(
		t,
		capture.Policy,
		capture.Receipts.ClockHighWaterMillis,
		0x77,
	)
	if err := installer.target.InstallReceiptBaseline(t.Context(), capture); err != nil {
		t.Fatal(err)
	}
	beforeLength, _, beforeEvicted := runtime.MutationLogStats()

	result, err := installer.Install(
		t.Context(),
		&receiptSnapshotTestStream{frames: frames, current: -1},
	)
	if err != nil || result.Header == nil {
		t.Fatalf("receipt Snapshot install with local retired evidence = %+v, %v", result, err)
	}
	afterLength, _, afterEvicted := runtime.MutationLogStats()
	if afterLength != beforeLength || afterEvicted != beforeEvicted+1 {
		t.Fatalf(
			"receipt Snapshot install WAL boundary: before=(%d,%d) after=(%d,%d)",
			beforeLength,
			beforeEvicted,
			afterLength,
			afterEvicted,
		)
	}
}

func TestReceiptSnapshotInstallerRejectsBeforePublication(t *testing.T) {
	valid, _ := receiptSnapshotCollectorFixture(t)
	tests := []struct {
		name   string
		frames func() []*pb.SnapshotResponse
		limits func() ReceiptSnapshotCollectorLimits
		ctx    func() context.Context
	}{
		{
			name: "downgrade",
			frames: func() []*pb.SnapshotResponse {
				frames := cloneReceiptSnapshotCollectorFrames(valid)
				frames[0].GetHeader().Format = pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
				frames[0].GetHeader().ReceiptMetadata = nil
				return frames
			},
		},
		{
			name: "corrupt footer",
			frames: func() []*pb.SnapshotResponse {
				frames := cloneReceiptSnapshotCollectorFrames(valid)
				frames[len(frames)-1].GetFooter().EdgeCount++
				return frames
			},
		},
		{
			name: "truncated",
			frames: func() []*pb.SnapshotResponse {
				return cloneReceiptSnapshotCollectorFrames(valid[:len(valid)-1])
			},
		},
		{
			name:   "capacity",
			frames: func() []*pb.SnapshotResponse { return cloneReceiptSnapshotCollectorFrames(valid) },
			limits: func() ReceiptSnapshotCollectorLimits {
				limits := receiptSnapshotCollectorLimits()
				limits.MaxFrames = uint64(len(valid) - 1)
				limits.MaxActiveReceipts = limits.MaxFrames
				limits.MaxRetiredEpochs = limits.MaxFrames
				limits.MaxRetiredReceipts = limits.MaxFrames
				limits.MaxGraphFrames = limits.MaxFrames
				return limits
			},
		},
		{
			name:   "canceled",
			frames: func() []*pb.SnapshotResponse { return cloneReceiptSnapshotCollectorFrames(valid) },
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			limits := receiptSnapshotCollectorLimits()
			if tc.limits != nil {
				limits = tc.limits()
			}
			installer, runtime := newReceiptSnapshotInstallerFixture(t, valid, limits)
			ctx := context.Background()
			if tc.ctx != nil {
				ctx = tc.ctx()
			}
			_, err := installer.Install(
				ctx,
				&receiptSnapshotTestStream{frames: tc.frames(), current: -1},
			)
			if err == nil {
				t.Fatal("invalid receipt Snapshot installed")
			}
			if tc.name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled install = %v", err)
			}
			if _, _, ok := runtime.GraphCache().GetEdgeDetail("tail", "head"); ok {
				t.Fatal("rejected receipt Snapshot mutated graph")
			}
			if length, _, evicted := runtime.MutationLogStats(); length != 0 || evicted != 0 {
				t.Fatalf("rejected receipt Snapshot changed log = len %d evicted %d", length, evicted)
			}
		})
	}
}

func TestReceiptSnapshotInstallerPublishesRetiredEvidence(t *testing.T) {
	frames, _ := receiptSnapshotCollectorFixtureWithRetired(t)
	installer, runtime := newReceiptSnapshotInstallerFixture(
		t, frames, receiptSnapshotCollectorLimits(),
	)
	result, err := installer.Install(
		context.Background(),
		&receiptSnapshotTestStream{frames: frames, current: -1},
	)
	if err != nil || result.Header == nil {
		t.Fatalf("receipt Snapshot retired evidence install = %+v, %v", result, err)
	}
	if _, _, ok := runtime.GraphCache().GetEdgeDetail("tail", "head"); !ok {
		t.Fatal("receipt Snapshot retired candidate did not publish graph")
	}
	if length, _, evicted := runtime.MutationLogStats(); length != 0 || evicted != 1 {
		t.Fatalf("receipt Snapshot retired candidate log = len %d evicted %d", length, evicted)
	}
}

func TestReceiptSnapshotInstallerWaitHonorsCancellation(t *testing.T) {
	frames, _ := receiptSnapshotCollectorFixture(t)
	installer, _ := newReceiptSnapshotInstallerFixture(
		t, frames, receiptSnapshotCollectorLimits(),
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	first := &receiptSnapshotTestStream{
		frames: cloneReceiptSnapshotCollectorFrames(frames), current: -1,
		before: func(int) {
			once.Do(func() {
				close(entered)
				<-release
			})
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := installer.Install(context.Background(), first)
		done <- err
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := installer.Install(
		ctx,
		&receiptSnapshotTestStream{frames: cloneReceiptSnapshotCollectorFrames(frames), current: -1},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting canceled install = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
