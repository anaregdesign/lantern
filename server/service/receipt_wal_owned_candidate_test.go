package service

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func writeReceiptWALClockJournal(t *testing.T, path string, config mutationreceipt.Config, highWater int64) {
	t.Helper()
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := mutationreceipt.CreateClockJournal(lease.Path(), config.Epoch, store.PolicyFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.Advance(highWater); err != nil {
		t.Fatal(err)
	}
}

func ownedReceiptWALFixture(t *testing.T) (string, mutationreceipt.Config, mutationlog.Entry) {
	t.Helper()
	config, receiptEntry := receiptWALAuditFixture(t)
	seed := recoveryGraphPutEffectEntry(t, 0x72, receiptEntry.HLC.WallNs-1,
		&pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{
			Edge: &pb.Edge{Tail: "tail", Head: "present", Weight: 1,
				Expiration: timestamppb.New(time.Now().Add(2 * time.Hour))},
		}}})
	path := writeReceiptWALAuditEntries(t, seed, receiptEntry)
	return path, config, receiptEntry
}

func TestOpenLeasedReceiptWALCandidateKeepsClockAndPath(t *testing.T) {
	path, config, receiptEntry := ownedReceiptWALFixture(t)
	receipt := receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts[0]
	// A previous unlogged Lookup expired this receipt. The WAL still has
	// its historical commit bytes, and the wall clock rolled backward.
	forward := receipt.DeadlineMillis + 1
	writeReceiptWALClockJournal(t, path, config, forward)
	now := time.UnixMilli(receipt.DeadlineMillis - 1)
	owner, err := openLeasedReceiptWALCandidate(path, config, now, mutationlog.Options{Capacity: 2}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if got := owner.state.receipts.Stats(); got.HighWaterMillis < forward || got.Entries != 0 {
		t.Fatalf("recovered Store ignored durable expiry: %+v", got)
	}
	if got := owner.journal.HighWaterMillis(); got < forward {
		t.Fatalf("recovered journal moved backward: %d", got)
	}
	// An aborted Begin never reaches the WAL, yet its clock transition must
	// survive another restart through the newly attached journal sink.
	abortedAt := forward + 123
	tx, err := owner.state.receipts.Begin(time.UnixMilli(abortedAt))
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	if got := owner.journal.HighWaterMillis(); got != abortedAt {
		t.Fatalf("aborted Begin was not durably journaled: got %d, want %d", got, abortedAt)
	}
	if seq, ok := owner.state.log.LastSeq(); !ok || seq != 2 {
		t.Fatalf("live Log frontier = %d, %v", seq, ok)
	}
	if weight, ok := owner.state.graph.GetWeight("tail", "present"); ok || weight != 0 {
		t.Fatalf("recovered graph missed Delete: %g, %v", weight, ok)
	}
	if competitor, err := mutationlog.AcquireFileWALLease(path); competitor != nil || !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
		if competitor != nil {
			_ = competitor.Close()
		}
		t.Fatalf("candidate released path while open: %p, %v", competitor, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.state.log.Append(&pb.Mutation{}, receiptEntry.HLC); !errors.Is(err, mutationlog.ErrClosed) {
		t.Fatalf("closed candidate Log append = %v", err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("candidate Close leaked lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := openLeasedReceiptWALCandidate(path, config, now, mutationlog.Options{Capacity: 2}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if got := resumed.state.receipts.Stats().HighWaterMillis; got != abortedAt {
		t.Fatalf("aborted Begin high-water was lost on second restart: got %d, want %d", got, abortedAt)
	}
}

func TestOpenLeasedReceiptWALCandidateRejectsMissingOrInvalidJournal(t *testing.T) {
	path, config, _ := ownedReceiptWALFixture(t)
	configure := func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil }
	if owner, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour, configure); owner != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing journal candidate = %p, %v", owner, err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("missing journal leaked path lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	writeReceiptWALClockJournal(t, path, config, time.Now().UnixMilli())
	wrong := config
	wrong.Epoch = mutationreceipt.Epoch{0x61}
	if owner, err := openLeasedReceiptWALCandidate(path, wrong, time.Now(), mutationlog.Options{}, time.Hour, configure); owner != nil || !errors.Is(err, mutationreceipt.ErrClockJournalBinding) {
		t.Fatalf("wrong-epoch candidate = %p, %v", owner, err)
	}
	lease, err = mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("wrong-epoch candidate leaked path lease: %v", err)
	}
	defer lease.Close()
}

func TestOpenLeasedReceiptWALCandidateRejectsLegacyEffectsAndReleasesOwner(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	path := writeReceiptWALAuditEntries(t, auditGraphEntry(1), receiptEntry)
	writeReceiptWALClockJournal(t, path, config, time.Now().UnixMilli())
	owner, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil })
	if owner != nil || !errors.Is(err, errReceiptWALUnion) || !strings.Contains(err.Error(), "accepted-effect evidence") {
		t.Fatalf("legacy-effect candidate = %p, %v", owner, err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("failed replay leaked path lease: %v", err)
	}
	defer lease.Close()
}

func TestMatchingReceiptWALLogTailRejectsPayloadDrift(t *testing.T) {
	first := auditGraphEntry(1)
	staged := mutationlog.New(mutationlog.Options{})
	live := mutationlog.New(mutationlog.Options{})
	defer staged.Close()
	defer live.Close()
	if _, err := staged.Append(first.Op, first.HLC); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Append(first.Op, first.HLC); err != nil {
		t.Fatal(err)
	}
	if err := matchingReceiptWALLogTail(staged, live); err != nil {
		t.Fatalf("matching tails = %v", err)
	}
	other := auditGraphEntry(1)
	other.Op.(*pb.Mutation).GetOp().GetPutVertex().Vertex.Key = "different"
	changed := mutationlog.New(mutationlog.Options{})
	defer changed.Close()
	if _, err := changed.Append(other.Op, other.HLC); err != nil {
		t.Fatal(err)
	}
	if err := matchingReceiptWALLogTail(staged, changed); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("changed tail payload = %v", err)
	}
}
