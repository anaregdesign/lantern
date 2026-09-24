package service

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func receiptWALAuditFixture(t *testing.T) (mutationreceipt.Config, mutationlog.Entry) {
	t.Helper()
	entry, envelope := receiptEdgeDeleteCodecFixture(t, nil)
	return mutationreceipt.Config{
		Epoch: envelope.Epoch, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
	}, entry
}

func writeReceiptWALAuditEntries(t *testing.T, entries ...mutationlog.Entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mixed.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range entries {
		entry.Seq = uint64(i + 1)
		if err := wal.Write(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func auditGraphEntry(seq uint64) mutationlog.Entry {
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertex{
		PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "graph-only"}},
	}})
	graph.Seq = seq
	graph.Hlc.Logical += uint32(seq - 1)
	return mutationlog.Entry{HLC: receiptWALUnionGraphHLC(graph), Op: graph}
}

func recoveryEdgeEntry(seq uint64) mutationlog.Entry {
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutEdge{
		PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{
			Tail: "tail", Head: "present", Weight: 1,
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		}},
	}})
	graph.Seq = seq
	graph.Hlc.Logical += uint32(seq - 1)
	return mutationlog.Entry{HLC: receiptWALUnionGraphHLC(graph), Op: graph}
}

func TestReceiptWALRecoveryCandidateReplaysDetachedOriginalResults(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	path := writeReceiptWALAuditEntries(t, auditGraphEntry(1), recoveryEdgeEntry(2), receiptEntry)
	recoveryTime := time.Now()
	candidate, err := resumeReceiptWALCandidate(path, config, recoveryTime, mutationlog.Options{Capacity: 2}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := candidate.graph.GetVertex("graph-only"); !ok || got.GetKey() != "graph-only" {
		t.Fatalf("recovered vertex = %v, %v", got, ok)
	}
	if weight, ok := candidate.graph.GetWeight("tail", "present"); ok || weight != 0 {
		t.Fatalf("receipt Delete did not remove Edge: %g, %v", weight, ok)
	}
	envelope := receiptEntry.Op.(*edgeDeleteReceiptEnvelope)
	var foundDeadline bool
	for _, tombstone := range candidate.graph.SnapshotReplication().Tombstones.Edges {
		if tombstone.Tail == "tail" && tombstone.Head == "present" {
			if !tombstone.Expiration.Equal(envelope.TombstoneExpiration) || !tombstone.HLC.Equal(envelope.HLC) {
				t.Fatalf("recovered tombstone = %+v, want deadline %v/HLC %+v", tombstone, envelope.TombstoneExpiration, envelope.HLC)
			}
			foundDeadline = true
		}
	}
	if !foundDeadline {
		t.Fatal("recovered graph omitted active original Delete tombstone")
	}
	oldHLC := envelope.HLC
	oldHLC.WallNs--
	if applied := candidate.graph.AddEdgeWithExpirationContribHLC("tail", "present", 1, time.Now().Add(time.Hour), graphcache.ContribID{1}, oldHLC); applied {
		t.Fatal("recovered tombstone admitted an older Add")
	}
	if seq, ok := candidate.log.LastSeq(); !ok || seq != 3 {
		t.Fatalf("recovered Log frontier = %d, %v", seq, ok)
	}
	if got := candidate.log.RetainedEntries(); len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("recovered Log ring = %+v", got)
	}
	if _, err := candidate.log.Append(&pb.Mutation{}, receiptEntry.HLC); !errors.Is(err, mutationlog.ErrClosed) {
		t.Fatalf("detached Log append = %v, want closed read-only Log", err)
	}
	if len(candidate.origins.States()) != 2 || !candidate.hlcFrontier.Equal(receiptEntry.HLC) {
		t.Fatalf("recovered origin/HLC frontier = %+v, %+v", candidate.origins.States(), candidate.hlcFrontier)
	}
	if highWater := candidate.receipts.Stats().HighWaterMillis; highWater < recoveryTime.UnixMilli() {
		t.Fatalf("recovered Store high-water = %d, before replay time %d", highWater, recoveryTime.UnixMilli())
	}
	want := receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts
	for i, receipt := range want {
		status, got, err := candidate.knownReceiptStatus(receipt.ID, time.Now())
		if err != nil || status != mutationreceipt.Confirmed || !reflect.DeepEqual(got.Result, receipt.Result) {
			t.Fatalf("receipt %d = %v, %+v, %v; want original %+v", i, status, got, err, receipt)
		}
	}
	if want[0].Result[0] != 1 || want[1].Result[0] != 0 || want[2].Result[0] != 0 {
		t.Fatalf("fixture lacks original true/false outcomes: %+v", want)
	}
	absent := want[0].ID
	absent[len(absent)-1] ^= 1
	status, got, err := candidate.knownReceiptStatus(absent, time.Now())
	if err != nil || status != mutationreceipt.NoLongerProvable || !reflect.DeepEqual(got, mutationreceipt.Receipt{}) {
		t.Fatalf("absent WAL ID = %v, %+v, %v; want UNKNOWN", status, got, err)
	}
}

func TestReceiptWALRecoveryCandidateRejectsUnrepresentableGraphHistory(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	deleteGraph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdge{
		DeleteEdge: &pb.DeleteEdgeRequest{Tail: "tail", Head: "present"},
	}})
	deleteGraph.Seq = 2
	deleteGraph.Hlc.Logical++
	deleteEntry := mutationlog.Entry{HLC: receiptWALUnionGraphHLC(deleteGraph), Op: deleteGraph}
	for _, tc := range []struct {
		name    string
		entries []mutationlog.Entry
	}{
		{"graph Delete deadline missing", []mutationlog.Entry{auditGraphEntry(1), deleteEntry, receiptEntry}},
		{"graph write after receipt", []mutationlog.Entry{receiptEntry, auditGraphEntry(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeReceiptWALAuditEntries(t, tc.entries...)
			candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
			if !errors.Is(err, errReceiptWALUnion) || candidate != nil {
				t.Fatalf("candidate = %p, %v; want no partially serving state", candidate, err)
			}
		})
	}
}

func TestReceiptWALRecoveryCandidateRejectsContradictoryAcceptedProjection(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	graphEntry := recoveryEdgeEntry(1)
	graph := graphEntry.Op.(*pb.Mutation)
	graph.Hlc.WallNs = receiptEntry.HLC.WallNs
	graph.Hlc.Logical = receiptEntry.HLC.Logical + 1
	graphEntry.HLC = receiptWALUnionGraphHLC(graph)
	path := writeReceiptWALAuditEntries(t, graphEntry, receiptEntry)
	candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
	if !errors.Is(err, errReceiptWALUnion) || candidate != nil {
		t.Fatalf("contradictory accepted Delete candidate = %p, %v; want fail-closed", candidate, err)
	}
}

func TestReceiptWALRecoveryCandidateRejectsCorruptAndIndeterminateTail(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
		want   error
	}{
		{"corrupt middle", func(b []byte) []byte { b[60] ^= 1; return b }, mutationlog.ErrFileWALCorrupt},
		{"torn tail", func(b []byte) []byte { return b[:len(b)-1] }, mutationlog.ErrFileWALTornTail},
		{"indeterminate partial append", func(b []byte) []byte { return append(b, 0, 0, 0) }, mutationlog.ErrFileWALTornTail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeReceiptWALAuditEntries(t, auditGraphEntry(1), receiptEntry)
			bytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.mutate(bytes), 0o600); err != nil {
				t.Fatal(err)
			}
			candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
			if !errors.Is(err, tc.want) || candidate != nil {
				t.Fatalf("candidate = %p, %v; want no partial state and %v", candidate, err, tc.want)
			}
		})
	}
}

func TestReceiptWALDecisionAuditMixedGenesisAndKnownResults(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	path := writeReceiptWALAuditEntries(t, auditGraphEntry(1), receiptEntry, auditGraphEntry(2))
	report, err := auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts
	if report.lastLocalSeq != 3 || len(report.origins) != 2 || len(report.knownReceipts) != len(want) {
		t.Fatalf("incomplete audit: %+v", report)
	}
	for i, receipt := range report.knownReceipts {
		if receipt.ID != want[i].ID || !reflect.DeepEqual(receipt.Result, want[i].Result) ||
			receipt.Index != want[i].Index || receipt.Group != want[i].Group {
			t.Fatalf("decision %d = %+v, want %+v", i, receipt, want[i])
		}
	}
	if report.origins[0].LastSeq != 2 || report.origins[1].LastSeq != 1 ||
		report.highWaterMillis < receiptEntry.HLC.WallNs/int64(time.Millisecond) {
		t.Fatalf("frontier/high-water = %+v", report)
	}
	// The report owns its result bytes, independent of a test fixture or
	// later buffer reuse by a replay caller.
	want[0].Result[0] ^= 1
	if report.knownReceipts[0].Result[0] == want[0].Result[0] {
		t.Fatal("audit aliased the decoded result")
	}
}

func TestReceiptWALDecisionAuditRejectsIncompleteOrMismatchedMetadata(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	envelope := receiptEntry.Op.(*edgeDeleteReceiptEnvelope)
	cases := []struct {
		name    string
		entries []mutationlog.Entry
		config  mutationreceipt.Config
	}{
		{"missing origin prefix", []mutationlog.Entry{auditGraphEntry(2)}, config},
		{"origin gap after receipt", []mutationlog.Entry{receiptEntry, auditGraphEntry(1), auditGraphEntry(3)}, config},
		{"frame HLC drift", []mutationlog.Entry{{HLC: hlc.Timestamp{WallNs: receiptEntry.HLC.WallNs + 1, NodeID: receiptEntry.HLC.NodeID}, Op: envelope}}, config},
		{"epoch mismatch", []mutationlog.Entry{receiptEntry}, mutationreceipt.Config{Epoch: mutationreceipt.Epoch{0x43}, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20}},
		{"policy mismatch", []mutationlog.Entry{receiptEntry}, mutationreceipt.Config{Epoch: config.Epoch, Retention: 2 * time.Hour, MaxEntries: 32, MaxBytes: 1 << 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeReceiptWALAuditEntries(t, tc.entries...)
			report, err := auditReceiptDecisionsFromFileWAL(path, tc.config, time.Now())
			if !errors.Is(err, errReceiptWALUnion) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
				t.Fatalf("audit = %+v, %v; want empty report and fail-closed error", report, err)
			}
		})
	}
}

func TestReceiptWALDecisionAuditRejectsConflictsAndCapacity(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	first := receiptEntry.Op.(*edgeDeleteReceiptEnvelope)
	second := cloneReceiptEdgeDeleteCodecEnvelope(first)
	second.OriginSeq++
	second.HLC.Logical++
	second.Mutation = receiptEdgeDeleteWALMutation(second)
	path := writeReceiptWALAuditEntries(t, receiptEntry, mutationlog.Entry{HLC: second.HLC, Op: second})
	if report, err := auditReceiptDecisionsFromFileWAL(path, config, time.Now()); !errors.Is(err, mutationreceipt.ErrInvalidSnapshot) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
		t.Fatalf("duplicate decision = %+v, %v", report, err)
	}

	limited := config
	limited.MaxEntries = len(first.Receipts) - 1
	policy, err := mutationreceipt.New(limited)
	if err != nil {
		t.Fatal(err)
	}
	tooMany := cloneReceiptEdgeDeleteCodecEnvelope(first)
	tooMany.PolicyFingerprint = policy.PolicyFingerprint()
	path = writeReceiptWALAuditEntries(t, mutationlog.Entry{HLC: tooMany.HLC, Op: tooMany})
	if report, err := auditReceiptDecisionsFromFileWAL(path, limited, time.Now()); !errors.Is(err, mutationreceipt.ErrCapacity) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
		t.Fatalf("over capacity = %+v, %v", report, err)
	}
	limitedBytes := config
	limitedBytes.MaxBytes = 1
	bytePolicy, err := mutationreceipt.New(limitedBytes)
	if err != nil {
		t.Fatal(err)
	}
	tooManyBytes := cloneReceiptEdgeDeleteCodecEnvelope(first)
	tooManyBytes.PolicyFingerprint = bytePolicy.PolicyFingerprint()
	path = writeReceiptWALAuditEntries(t, mutationlog.Entry{HLC: tooManyBytes.HLC, Op: tooManyBytes})
	if report, err := auditReceiptDecisionsFromFileWAL(path, limitedBytes, time.Now()); !errors.Is(err, mutationreceipt.ErrCapacity) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
		t.Fatalf("over byte capacity = %+v, %v", report, err)
	}

	// The envelope is structurally valid with a two-hour deadline, but its
	// fingerprint still names the one-hour configured policy. Expiry must
	// not hide that mismatch from the WAL audit.
	wrongRetention := cloneReceiptEdgeDeleteCodecEnvelope(first)
	for i := range wrongRetention.Receipts {
		wrongRetention.Receipts[i].DeadlineMillis += int64(time.Hour / time.Millisecond)
	}
	path = writeReceiptWALAuditEntries(t, mutationlog.Entry{HLC: wrongRetention.HLC, Op: wrongRetention})
	if report, err := auditReceiptDecisionsFromFileWAL(path, config, time.Now().Add(3*time.Hour)); !errors.Is(err, errReceiptWALUnion) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
		t.Fatalf("expired retention mismatch = %+v, %v", report, err)
	}
}

func TestReceiptWALDecisionAuditReleasesExpiredCapacityBeforeLaterReceipt(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	first := cloneReceiptEdgeDeleteCodecEnvelope(receiptEntry.Op.(*edgeDeleteReceiptEnvelope))
	config.MaxEntries = len(first.Receipts)
	config.MaxBytes = uint64(config.MaxEntries) * receiptWALDecisionCost(first.Receipts[0])
	policy, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	first.PolicyFingerprint = policy.PolicyFingerprint()
	later := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Millisecond)
	second := cloneReceiptEdgeDeleteCodecEnvelope(first)
	second.OriginSeq++
	second.HLC.WallNs = later.UnixNano()
	second.HLC.Logical = 0
	second.TombstoneExpiration = later.Add(time.Hour)
	for i := range second.Receipts {
		id, err := mutationreceipt.NewID(config.Epoch, later, [24]byte{byte(i + 11)})
		if err != nil {
			t.Fatal(err)
		}
		second.Receipts[i].ID = id
		second.Receipts[i].Group = mutationreceipt.GroupID{0x7e}
		second.Receipts[i].DeadlineMillis = later.Add(time.Hour).UnixMilli()
	}
	second.Mutation = receiptEdgeDeleteWALMutation(second)

	for _, tc := range []struct {
		name      string
		withGraph bool
	}{
		{"later graph HLC", true},
		{"same receipt frame HLC", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []mutationlog.Entry{{HLC: first.HLC, Op: first}}
			if tc.withGraph {
				graph := auditGraphEntry(1)
				graph.Op.(*pb.Mutation).Hlc.WallNs = later.Add(-time.Millisecond).UnixNano()
				graph.HLC = receiptWALUnionGraphHLC(graph.Op.(*pb.Mutation))
				entries = append(entries, graph)
			}
			entries = append(entries, mutationlog.Entry{HLC: second.HLC, Op: second})
			path := writeReceiptWALAuditEntries(t, entries...)
			report, err := auditReceiptDecisionsFromFileWAL(path, config, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if report.lastLocalSeq != uint64(len(entries)) || len(report.knownReceipts) != len(second.Receipts) {
				t.Fatalf("later decisions = %+v", report)
			}
			for i, got := range report.knownReceipts {
				if got.ID != second.Receipts[i].ID || !reflect.DeepEqual(got.Result, second.Receipts[i].Result) {
					t.Fatalf("known receipt %d = %+v, want %+v", i, got, second.Receipts[i])
				}
			}
		})
	}
}

func TestReceiptWALDecisionAuditExpiredAndTornTail(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	path := writeReceiptWALAuditEntries(t, receiptEntry)
	late := time.UnixMilli(receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts[0].DeadlineMillis + 1)
	report, err := auditReceiptDecisionsFromFileWAL(path, config, late)
	if err != nil || len(report.knownReceipts) != 0 || report.lastLocalSeq != 1 {
		t.Fatalf("expired audit = %+v, %v", report, err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, stat.Size()-1); err != nil {
		t.Fatal(err)
	}
	report, err = auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if !errors.Is(err, mutationlog.ErrFileWALTornTail) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
		t.Fatalf("torn WAL = %+v, %v", report, err)
	}
}
