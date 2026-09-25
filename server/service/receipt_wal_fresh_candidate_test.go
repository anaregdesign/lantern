package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestCreateLeasedReceiptWALCandidateFreshRestartAndExclusiveOwner(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	config.ClockHighWater = time.Now().Add(time.Minute)
	path := filepath.Join(t.TempDir(), "fresh.wal")
	configure := func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil }
	owner, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{Capacity: 2}, time.Hour, configure)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if seq, ok := owner.state.log.LastSeq(); ok || seq != 0 {
		t.Fatalf("fresh Log = %d, %v", seq, ok)
	}
	if seq, _, verified := owner.tip.Frontier(); !verified || seq != 0 {
		t.Fatalf("fresh tip = %d, verified %v", seq, verified)
	}
	if got := owner.journal.HighWaterMillis(); got != config.ClockHighWater.UnixMilli() {
		t.Fatalf("fresh clock high-water = %d, want %d", got, config.ClockHighWater.UnixMilli())
	}
	if competitor, err := mutationlog.AcquireFileWALLease(path); competitor != nil || !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
		if competitor != nil {
			_ = competitor.Close()
		}
		t.Fatalf("fresh owner lost lease: %p, %v", competitor, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if candidate, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure); candidate != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing WAL treated as fresh: %p, %v", candidate, err)
	}
	restored, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{Capacity: 2}, time.Hour, configure)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if seq, ok := restored.state.log.LastSeq(); ok || seq != 0 {
		t.Fatalf("restarted empty Log = %d, %v", seq, ok)
	}
	if got := restored.state.receipts.Stats().HighWaterMillis; got < config.ClockHighWater.UnixMilli() {
		t.Fatalf("restart moved clock backward: %d", got)
	}
}

func TestCreateLeasedReceiptWALCandidateFailsBeforeDiskForInvalidGraph(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	path := filepath.Join(t.TempDir(), "invalid-graph.wal")
	configurationFailure := errors.New("index policy failed")
	for _, configure := range []func(*graphcache.GraphCache[string, *pb.Vertex]) error{
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return configurationFailure },
		func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			return graph.PutVertex("unwanted", &pb.Vertex{})
		},
	} {
		owner, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure)
		if owner != nil || err == nil {
			t.Fatalf("invalid graph yielded owner=%p, err=%v", owner, err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid graph created a WAL: %v", err)
		}
	}
}

func TestCreateLeasedReceiptWALCandidateLeavesPartialFilesFailClosed(t *testing.T) {
	for _, sidecar := range []string{".tip", ".clock"} {
		t.Run(sidecar, func(t *testing.T) {
			config, _ := receiptWALAuditFixture(t)
			path := filepath.Join(t.TempDir(), "partial.wal")
			if err := os.WriteFile(path+sidecar, []byte("stale sidecar"), 0o600); err != nil {
				t.Fatal(err)
			}
			configure := func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil }
			owner, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure)
			if owner != nil || !errors.Is(err, os.ErrExist) {
				t.Fatalf("partial creation = %p, %v", owner, err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("partial WAL was removed instead of inspected: %v", err)
			}
			lease, err := mutationlog.AcquireFileWALLease(path)
			if err != nil {
				t.Fatalf("partial creation leaked lease: %v", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			if candidate, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure); candidate != nil || !errors.Is(err, os.ErrExist) {
				t.Fatalf("partial WAL silently reinitialized: %p, %v", candidate, err)
			}
			if candidate, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour, configure); candidate != nil || err == nil {
				t.Fatalf("partial WAL served after failed creation: %p, %v", candidate, err)
			}
		})
	}
}
