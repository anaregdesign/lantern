package service

import (
	"bytes"
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

func auditGraphPutEffectEntry(t *testing.T, seq uint64, outcomes ...graphcache.PutOutcome) mutationlog.Entry {
	t.Helper()
	entry := auditGraphEntry(seq)
	m := entry.Op.(*pb.Mutation)
	effect, err := newGraphPutEffectEnvelope(m, outcomes)
	if err != nil {
		t.Fatal(err)
	}
	entry.Op = effect
	return entry
}

func auditGraphAddEntry(seq uint64) mutationlog.Entry {
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_AddEdge{
		AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "tail", Head: "head", Weight: 1}},
	}})
	graph.Seq = seq
	graph.Hlc.Logical += uint32(seq - 1)
	return mutationlog.Entry{HLC: receiptWALUnionGraphHLC(graph), Op: graph}
}

func auditGraphAddEffectEntry(t *testing.T, seq uint64, accepted bool) mutationlog.Entry {
	t.Helper()
	entry := auditGraphAddEntry(seq)
	m := entry.Op.(*pb.Mutation)
	effect, err := newGraphAddEffectEnvelope(m, []bool{accepted})
	if err != nil {
		t.Fatal(err)
	}
	entry.Op = effect
	return entry
}

func recoveryGraphAddEffectEntry(t *testing.T, seq uint64, op *pb.MutationOp, accepted ...bool) mutationlog.Entry {
	t.Helper()
	m := receiptWALUnionGraphFixture(op)
	m.Seq = seq
	m.Hlc.Logical += uint32(seq - 1)
	effect, err := newGraphAddEffectEnvelope(m, accepted)
	if err != nil {
		t.Fatal(err)
	}
	return mutationlog.Entry{HLC: receiptWALUnionGraphHLC(m), Op: effect}
}

func recoveryGraphDeleteEffectEntry(t *testing.T, origin byte, seq uint64, wall int64, op *pb.MutationOp, deadline time.Time, accepted ...int) mutationlog.Entry {
	t.Helper()
	m := receiptWALUnionGraphFixture(op)
	m.Origin = bytes.Repeat([]byte{origin}, 16)
	m.Hlc.NodeId = append([]byte(nil), m.Origin...)
	m.Hlc.WallNs = wall
	m.Seq = seq
	m.TombstoneExpiration = timestamppb.New(deadline)
	effect, err := newGraphDeleteEffectEnvelope(m, accepted)
	if err != nil {
		t.Fatal(err)
	}
	return mutationlog.Entry{HLC: receiptWALUnionGraphHLC(m), Op: effect}
}

func recoveryGraphPutEffectEntry(t *testing.T, origin byte, wall int64, op *pb.MutationOp) mutationlog.Entry {
	t.Helper()
	m := receiptWALUnionGraphFixture(op)
	m.Origin = bytes.Repeat([]byte{origin}, 16)
	m.Hlc.NodeId = append([]byte(nil), m.Origin...)
	m.Hlc.WallNs = wall
	m.Seq = 1
	effect, err := newGraphPutEffectEnvelope(m, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive})
	if err != nil {
		t.Fatal(err)
	}
	return mutationlog.Entry{HLC: receiptWALUnionGraphHLC(m), Op: effect}
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

func TestReceiptWALRecoveryCandidateReplaysOnlyAcceptedPutEffects(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	future := timestamppb.New(time.Now().Add(time.Hour))
	past := timestamppb.New(time.Now().Add(-time.Hour))
	cases := []struct {
		name     string
		op       *pb.MutationOp
		outcomes []graphcache.PutOutcome
		live     string
		barriers []string
		omitted  string
	}{
		{"Vertex mixed and expired since commit", &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{nil, {Key: "live", Expiration: future}, {Key: "barrier", Expiration: past},
				{Key: "omitted", Expiration: future}, {Key: "since-expired", Expiration: past}},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeExpired,
			graphcache.PutOutcomeSuperseded, graphcache.PutOutcomeAppliedAndLive}, "live", []string{"barrier", "since-expired"}, "omitted"},
		{"Edge mixed", &pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{
			Edges: []*pb.Edge{nil, {Tail: "t", Head: "live", Weight: 2, Expiration: future},
				{Tail: "t", Head: "barrier", Weight: 3, Expiration: past}, {Tail: "t", Head: "omitted", Weight: 4, Expiration: future}},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeExpired,
			graphcache.PutOutcomeSuperseded}, "live", []string{"barrier"}, "omitted"},
		{"Vertex zero accepted", &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "omitted", Expiration: future},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeSuperseded}, "", nil, "omitted"},
		{"replicated Vertex live and barrier", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{
			Entries: []*pb.ReplicatedPutVertex{{Outcome: &pb.ReplicatedPutVertex_Live{Live: &pb.Vertex{Key: "live", Expiration: future}}},
				{Outcome: &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: &pb.VertexCausalBarrier{Key: "barrier"}}}},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeExpired}, "live", []string{"barrier"}, ""},
		{"replicated Edge live and barrier", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{
			Entries: []*pb.ReplicatedPutEdge{{Outcome: &pb.ReplicatedPutEdge_Live{Live: &pb.Edge{Tail: "t", Head: "live", Weight: 2, Expiration: future}}},
				{Outcome: &pb.ReplicatedPutEdge_CausalBarrier{CausalBarrier: &pb.EdgeCausalBarrier{Tail: "t", Head: "barrier"}}}},
		}}}, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive, graphcache.PutOutcomeExpired}, "live", []string{"barrier"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutation := receiptWALUnionGraphFixture(tc.op)
			mutation.Seq = 1
			effect, err := newGraphPutEffectEnvelope(mutation, tc.outcomes)
			if err != nil {
				t.Fatal(err)
			}
			path := writeReceiptWALAuditEntries(t, mutationlog.Entry{HLC: receiptWALUnionGraphHLC(mutation), Op: effect})
			candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			isEdge := strings.Contains(tc.name, "Edge")
			if tc.live != "" {
				if isEdge {
					if got, ok := candidate.graph.GetWeight("t", tc.live); !ok || got != 2 {
						t.Fatalf("recovered live Edge = %g, %v", got, ok)
					}
				} else if got, ok := candidate.graph.GetVertex(tc.live); !ok || got.GetKey() != tc.live {
					t.Fatalf("recovered live Vertex = %v, %v", got, ok)
				}
			}
			if tc.omitted != "" {
				if isEdge {
					if _, ok := candidate.graph.GetWeight("t", tc.omitted); ok {
						t.Fatal("omitted Edge Put was resurrected without its expired floor")
					}
				} else if _, ok := candidate.graph.GetVertex(tc.omitted); ok {
					t.Fatal("omitted Vertex Put was resurrected without its expired floor")
				}
			}
			barriers := candidate.graph.SnapshotReplication().Barriers
			for _, key := range tc.barriers {
				found := false
				if isEdge {
					for _, barrier := range barriers.Edges {
						found = found || barrier.Tail == "t" && barrier.Head == key
					}
				} else {
					for _, barrier := range barriers.Vertices {
						found = found || barrier.Key == key
					}
				}
				if !found {
					t.Fatalf("accepted Put barrier %q was lost", key)
				}
			}
			entries := candidate.log.RetainedEntries()
			if len(entries) != 1 || entries[0].Seq != 1 {
				t.Fatalf("recovered log positions = %+v", entries)
			}
			if _, ok := entries[0].Op.(*graphPutEffectEnvelope); !ok {
				t.Fatalf("recovered log lost the original effect envelope: %T", entries[0].Op)
			}
		})
	}
	path := writeReceiptWALAuditEntries(t, receiptEntry, auditGraphPutEffectEntry(t, 1, graphcache.PutOutcomeAppliedAndLive))
	if candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour); err != nil || candidate == nil {
		t.Fatalf("evidenced graph Put after receipt = %p, %v", candidate, err)
	}
}

func TestReceiptWALRecoveryCandidateReplaysOnlyAcceptedAddEffects(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	valid := timestamppb.New(time.Now().Add(time.Hour))
	expired := timestamppb.New(time.Now().Add(-time.Hour))
	explicit := graphcache.ContribID{0x92}
	op := &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{
		Edges: []*pb.Edge{nil,
			{Tail: "t", Head: "explicit", Weight: 2, Expiration: valid},
			{Tail: "t", Head: "omitted", Weight: 3, Expiration: valid},
			{Tail: "t", Head: "expired", Weight: 4, Expiration: expired},
			nil, {Tail: "t", Head: "synthetic", Weight: 5, Expiration: valid}},
		ContribIds: [][]byte{nil, explicit[:]},
	}}}
	entry := recoveryGraphAddEffectEntry(t, 1, op, true, false, true, true)
	effect := entry.Op.(*graphAddEffectEnvelope)
	if !reflect.DeepEqual(effect.AcceptedIndexes, []uint32{1, 3, 5}) {
		t.Fatalf("accepted wire indexes = %v", effect.AcceptedIndexes)
	}
	path := writeReceiptWALAuditEntries(t, receiptEntry, entry)
	candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		head string
		want float32
		live bool
		id   graphcache.ContribID
	}{
		{"explicit", 2, true, explicit},
		{"omitted", 0, false, graphcache.ContribID{}},
		{"expired", 0, false, graphcache.ContribID{}},
		{"synthetic", 5, true, contribIDFor(effect.Mutation.Origin, effect.Mutation.Seq, 5)},
	} {
		got, ok := candidate.graph.GetWeight("t", tc.head)
		if ok != tc.live || (ok && got != tc.want) {
			t.Fatalf("recovered Add %s = %g, %v; want %g, %v", tc.head, got, ok, tc.want, tc.live)
		}
		if tc.live {
			found := false
			for _, edge := range candidate.graph.SnapshotReplication().Graph.Edges {
				if edge.Tail == "t" && edge.Head == tc.head && len(edge.Contributions) == 1 && edge.Contributions[0].ContribID == tc.id {
					found = true
				}
			}
			if !found {
				t.Fatalf("recovered Add %s lost original ContribID %x", tc.head, tc.id)
			}
		}
	}
	if seq, ok := candidate.log.LastSeq(); !ok || seq != 2 || len(candidate.origins.States()) != 2 {
		t.Fatalf("recovered Add log/origin frontier = %d, %v, %+v", seq, ok, candidate.origins.States())
	}
	got, ok := candidate.log.RetainedEntries()[1].Op.(*graphAddEffectEnvelope)
	if !ok || !proto.Equal(got.Mutation, effect.Mutation) || !reflect.DeepEqual(got.AcceptedIndexes, effect.AcceptedIndexes) {
		t.Fatalf("WAL/Subscribe projection changed: %T", candidate.log.RetainedEntries()[1].Op)
	}
}

func TestReceiptWALRecoveryCandidatePreservesAddOmissionsAcrossDelete(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	valid := timestamppb.New(time.Now().Add(time.Hour))
	add := func(seq uint64, accepted bool) mutationlog.Entry {
		return recoveryGraphAddEffectEntry(t, seq, &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "tail", Head: "present", Weight: 1, Expiration: valid},
		}}}, accepted)
	}
	path := writeReceiptWALAuditEntries(t, add(1, true), receiptEntry, add(2, false))
	candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := candidate.graph.GetWeight("tail", "present"); ok || got != 0 {
		t.Fatalf("omitted post-Delete Add was resurrected: %g, %v", got, ok)
	}
	for _, receipt := range receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts {
		status, _, err := candidate.knownReceiptStatus(receipt.ID, time.Now())
		if err != nil || status != mutationreceipt.Confirmed {
			t.Fatalf("recovered original Delete result = %v, %v", status, err)
		}
	}
	if seq, ok := candidate.log.LastSeq(); !ok || seq != 3 {
		t.Fatalf("mixed Add/Delete log frontier = %d, %v", seq, ok)
	}
}

func TestReceiptWALRecoveryCandidateRejectsContradictoryAddEffect(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	put := recoveryEdgeEntry(1)
	add := recoveryGraphAddEffectEntry(t, 1, &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "tail", Head: "present", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
	}}}, true)
	m := add.Op.(*graphAddEffectEnvelope).Mutation
	m.Origin = bytes.Repeat([]byte{0x32}, 16)
	m.Hlc.NodeId = append([]byte(nil), m.Origin...)
	m.Hlc.WallNs = put.HLC.WallNs - 1
	add.HLC = receiptWALUnionGraphHLC(m)
	path := writeReceiptWALAuditEntries(t, put, add)
	if candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour); candidate != nil || !errors.Is(err, errReceiptWALUnion) || !strings.Contains(err.Error(), "replayed as rejected") {
		t.Fatalf("contradictory accepted Add = %p, %v; want no candidate", candidate, err)
	}
}

func TestReceiptWALRecoveryCandidateRejectsContradictoryDuplicateAddEffect(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	id := graphcache.ContribID{0xa3}
	op := &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
		Edge:      &pb.Edge{Tail: "t", Head: "h", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		ContribId: id[:],
	}}}
	first := recoveryGraphAddEffectEntry(t, 1, op, true)
	second := recoveryGraphAddEffectEntry(t, 2, op, true)
	path := writeReceiptWALAuditEntries(t, first, second)
	if candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour); candidate != nil || !errors.Is(err, errReceiptWALUnion) || !strings.Contains(err.Error(), "replayed as rejected") {
		t.Fatalf("contradictory duplicate Add = %p, %v; want no candidate", candidate, err)
	}
}

func TestReceiptWALRecoveryCandidateRejectsRepeatedAddFrameAfterAmbiguousAppend(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	entry := auditGraphAddEffectEntry(t, 1, true)
	path := writeReceiptWALAuditEntries(t, entry, entry)
	if candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour); candidate != nil || !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("duplicate origin Add frame = %p, %v; want no candidate", candidate, err)
	}
}

func TestReceiptWALRecoveryCandidateReplaysAcceptedVertexDeleteEffects(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	wall := time.Now().UnixNano()
	deadline := time.Now().Add(time.Hour)
	valid := timestamppb.New(deadline)
	put := func(origin byte, wall int64, key string) mutationlog.Entry {
		return recoveryGraphPutEffectEntry(t, origin, wall, &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Expiration: valid}},
		}})
	}
	deletion := recoveryGraphDeleteEffectEntry(t, 0x63, 1, wall, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"present", "absent", "omitted", "present"}},
	}}, deadline, 0, 1, 3)
	path := writeReceiptWALAuditEntries(t, put(0x61, wall-2, "present"), put(0x62, wall+2, "omitted"), deletion)
	candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := candidate.graph.GetVertex("present"); ok {
		t.Fatal("accepted Vertex Delete did not remove present key")
	}
	if _, ok := candidate.graph.GetVertex("omitted"); !ok {
		t.Fatal("omitted Vertex Delete removed newer value")
	}
	seen := map[string]bool{}
	for _, tombstone := range candidate.graph.SnapshotReplication().Tombstones.Vertices {
		if !tombstone.Expiration.Equal(deadline) {
			t.Fatalf("Vertex Delete renewed original deadline: %+v", tombstone)
		}
		seen[tombstone.Key] = true
	}
	if !seen["present"] || !seen["absent"] || seen["omitted"] || len(seen) != 2 {
		t.Fatalf("accepted Vertex Delete tombstones = %+v", seen)
	}
	if seq, ok := candidate.log.LastSeq(); !ok || seq != 3 || len(candidate.origins.States()) != 3 {
		t.Fatalf("Vertex Delete log/origin frontier = %d, %v, %+v", seq, ok, candidate.origins.States())
	}
}

func TestReceiptWALRecoveryCandidateReplaysAcceptedEdgeDeleteEffects(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	wall := time.Now().UnixNano()
	deadline := time.Now().Add(time.Hour)
	valid := timestamppb.New(deadline)
	put := func(origin byte, wall int64, head string) mutationlog.Entry {
		return recoveryGraphPutEffectEntry(t, origin, wall, &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
			PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: head, Weight: 2, Expiration: valid}},
		}})
	}
	deletion := recoveryGraphDeleteEffectEntry(t, 0x66, 1, wall, &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
		DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{
			{Tail: "t", Head: "present"}, {Tail: "t", Head: "absent"},
			{Tail: "t", Head: "omitted"}, {Tail: "t", Head: "present"},
		}},
	}}, deadline, 0, 1, 3)
	path := writeReceiptWALAuditEntries(t, put(0x64, wall-2, "present"), put(0x65, wall+2, "omitted"), recoveryEdgeEntry(1), receiptEntry, deletion)
	candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := candidate.graph.GetWeight("t", "present"); ok || got != 0 {
		t.Fatalf("accepted Edge Delete left present edge: %g, %v", got, ok)
	}
	if got, ok := candidate.graph.GetWeight("t", "omitted"); !ok || got != 2 {
		t.Fatalf("omitted Edge Delete removed newer row: %g, %v", got, ok)
	}
	seen := map[string]bool{}
	for _, tombstone := range candidate.graph.SnapshotReplication().Tombstones.Edges {
		if tombstone.Tail == "t" {
			if !tombstone.Expiration.Equal(deadline) {
				t.Fatalf("Edge Delete renewed original deadline: %+v", tombstone)
			}
			seen[tombstone.Head] = true
		}
	}
	if !seen["present"] || !seen["absent"] || seen["omitted"] || len(seen) != 2 {
		t.Fatalf("accepted Edge Delete tombstones = %+v", seen)
	}
	if seq, ok := candidate.log.LastSeq(); !ok || seq != 5 || len(candidate.origins.States()) != 5 {
		t.Fatalf("Edge Delete log/origin frontier = %d, %v, %+v", seq, ok, candidate.origins.States())
	}
	for _, receipt := range receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts {
		status, _, err := candidate.knownReceiptStatus(receipt.ID, time.Now())
		if err != nil || status != mutationreceipt.Confirmed {
			t.Fatalf("interleaved receipt = %v, %v", status, err)
		}
	}
}

func TestReceiptWALRecoveryCandidateReplaysZeroAcceptedDeleteEffect(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	entry := recoveryGraphDeleteEffectEntry(t, 0x68, 1, time.Now().UnixNano(), &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"omitted"}},
	}}, time.Now().Add(time.Hour))
	path := writeReceiptWALAuditEntries(t, recoveryEdgeEntry(1), receiptEntry, entry)
	candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.graph.SnapshotReplication().Tombstones.Vertices) != 0 {
		t.Fatal("zero-accepted Delete created a tombstone")
	}
	if seq, ok := candidate.log.LastSeq(); !ok || seq != 3 {
		t.Fatalf("zero-accepted Delete log frontier = %d, %v", seq, ok)
	}
}

func TestReceiptWALRecoveryCandidateRejectsContradictoryDeleteEffect(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	wall := time.Now().UnixNano()
	put := recoveryGraphPutEffectEntry(t, 0x69, wall+1, &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
		PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "protected", Expiration: timestamppb.New(time.Now().Add(time.Hour))}},
	}})
	deletion := recoveryGraphDeleteEffectEntry(t, 0x6a, 1, wall, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"protected"}},
	}}, time.Now().Add(time.Hour), 0)
	path := writeReceiptWALAuditEntries(t, put, deletion)
	if candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour); candidate != nil || !errors.Is(err, errReceiptWALUnion) || !strings.Contains(err.Error(), "replayed as rejected") {
		t.Fatalf("contradictory accepted Delete = %p, %v; want no candidate", candidate, err)
	}
}

func TestReplayGraphDeleteEffectPreservesExpiredDeadline(t *testing.T) {
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	wall := time.Now().Add(-2 * time.Hour).UnixNano()
	if !graph.PutEdgeWithExpirationHLC("t", "h", 1, time.Now().Add(time.Hour), hlc.Timestamp{WallNs: wall - 1}) {
		t.Fatal("failed to seed Edge")
	}
	entry := recoveryGraphDeleteEffectEntry(t, 0x6b, 1, wall, &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
		DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "t", Head: "h"}}},
	}}, time.Now().Add(-time.Hour), 0)
	if err := replayGraphDeleteEffect(graph, entry.Op.(*graphDeleteEffectEnvelope)); err != nil {
		t.Fatal(err)
	}
	if got, ok := graph.GetWeight("t", "h"); ok || got != 0 {
		t.Fatalf("expired-deadline Delete left Edge: %g, %v", got, ok)
	}
	if len(graph.SnapshotReplication().Tombstones.Edges) != 0 {
		t.Fatal("replay renewed an already expired Delete tombstone")
	}
}

func TestReplayGraphDeleteEffectRejectsCapacityBeforePartialGraphChange(t *testing.T) {
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	graph.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
	entry := recoveryGraphDeleteEffectEntry(t, 0x6c, 1, time.Now().UnixNano(), &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
		DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "t", Head: "a"}, {Tail: "t", Head: "b"}}},
	}}, time.Now().Add(time.Hour), 0, 1)
	if err := replayGraphDeleteEffect(graph, entry.Op.(*graphDeleteEffectEnvelope)); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("over-budget Delete replay = %v, want fail-closed", err)
	}
	if len(graph.SnapshotReplication().Tombstones.Edges) != 0 {
		t.Fatal("over-budget Delete partially changed causal state")
	}
}

func TestReceiptWALRecoveryCandidateRejectsContradictoryPutEffect(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	first := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "same"}}}})
	first.Seq = 1
	firstEffect, err := newGraphPutEffectEnvelope(first, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive})
	if err != nil {
		t.Fatal(err)
	}
	second := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "same"}}}})
	second.Seq = 1
	second.Origin = bytes.Repeat([]byte{0x32}, 16)
	second.Hlc.NodeId = append([]byte(nil), second.Origin...)
	second.Hlc.WallNs--
	secondEffect, err := newGraphPutEffectEnvelope(second, []graphcache.PutOutcome{graphcache.PutOutcomeAppliedAndLive})
	if err != nil {
		t.Fatal(err)
	}
	path := writeReceiptWALAuditEntries(t,
		mutationlog.Entry{HLC: receiptWALUnionGraphHLC(first), Op: firstEffect},
		mutationlog.Entry{HLC: receiptWALUnionGraphHLC(second), Op: secondEffect})
	candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour)
	if candidate != nil || !errors.Is(err, errReceiptWALUnion) || !strings.Contains(err.Error(), "replayed as") {
		t.Fatalf("contradictory accepted Put = %p, %v; want no candidate", candidate, err)
	}
}

func TestReceiptWALRecoveryCandidateRejectsRepeatedPutFrameAfterAmbiguousAppend(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	entry := auditGraphPutEffectEntry(t, 1, graphcache.PutOutcomeAppliedAndLive)
	path := writeReceiptWALAuditEntries(t, entry, entry)
	if candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour); candidate != nil || !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("duplicate origin Put frame = %p, %v; want no candidate", candidate, err)
	}
}

func TestReceiptWALRecoveryCandidateRejectsUnrepresentableGraphHistory(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	for _, tc := range []struct {
		name    string
		entries []mutationlog.Entry
	}{
		{"graph write after receipt", []mutationlog.Entry{receiptEntry, auditGraphEntry(1)}},
		{"raw graph Add after receipt", []mutationlog.Entry{receiptEntry, auditGraphAddEntry(1)}},
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

func TestReceiptWALRecoveryCandidateRejectsOldGraphDeleteVersionWithoutPartialState(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	deleteGraph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"absent"}},
	}})
	deleteGraph.Seq = 2
	deleteGraph.Hlc.Logical++
	// FileWAL computes a valid CRC around this old v2 payload. Merely
	// detecting corrupt bytes is insufficient: the version itself must be
	// refused because v2 carried no accepted-index decision.
	encodeOld := func(op mutationlog.MutationOp) ([]byte, error) {
		m := op.(*pb.Mutation)
		if m.GetSeq() == 1 {
			return encodeReceiptWALUnion(m)
		}
		body, err := encodeReceiptWALGraph(m)
		if err != nil {
			return nil, err
		}
		raw := make([]byte, receiptWALUnionHeaderSize+len(body))
		copy(raw[:8], "LRWU\x02\x00\x00\x00")
		raw[8] = receiptWALUnionGraph
		binary.BigEndian.PutUint32(raw[12:16], uint32(len(body)))
		copy(raw[16:], body)
		return raw, nil
	}
	path := filepath.Join(t.TempDir(), "old-delete.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeOld)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range []*pb.Mutation{auditGraphEntry(1).Op.(*pb.Mutation), deleteGraph} {
		if err := wal.Write(mutationlog.Entry{Seq: uint64(i + 1), HLC: receiptWALUnionGraphHLC(m), Op: m}); err != nil {
			t.Fatal(err)
		}
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	if candidate, err := resumeReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour); candidate != nil || err == nil || !strings.Contains(err.Error(), "decode seq 2") {
		t.Fatalf("v2 Delete recovery = %p, %v; want no candidate", candidate, err)
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
	path := writeReceiptWALAuditEntries(t, auditGraphEntry(1), receiptEntry,
		auditGraphPutEffectEntry(t, 2, graphcache.PutOutcomeAppliedAndLive))
	report, err := auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts
	if report.lastLocalSeq != 3 || len(report.origins) != 2 || len(report.knownReceipts) != len(want) {
		t.Fatalf("incomplete audit: %+v", report)
	}
	if report.unprovenGraphPutRows != 1 || report.evidencedGraphPutRows != 1 {
		t.Fatalf("Put evidence inventory = %+v", report)
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

func TestReceiptWALDecisionAuditRejectsOldPutAfterReceiptWithoutPartialReport(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	path := writeReceiptWALAuditEntries(t, receiptEntry, auditGraphEntry(1))
	report, err := auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if !errors.Is(err, errReceiptWALUnion) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
		t.Fatalf("old Put audit = %+v, %v; want no certified partial report", report, err)
	}
	// A zero-accepted sidecar still proves the local result. It does not
	// authorize serving replay or receipt admission.
	path = writeReceiptWALAuditEntries(t, receiptEntry,
		auditGraphPutEffectEntry(t, 1, graphcache.PutOutcomeSuperseded))
	report, err = auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if err != nil || report.evidencedGraphPutRows != 1 || report.unprovenGraphPutRows != 0 || report.lastLocalSeq != 2 {
		t.Fatalf("zero-accepted Put audit = %+v, %v", report, err)
	}
}

func TestReceiptWALDecisionAuditRejectsOldAddAfterReceiptWithoutPartialReport(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	path := writeReceiptWALAuditEntries(t, auditGraphAddEntry(1), receiptEntry,
		auditGraphAddEffectEntry(t, 2, true))
	report, err := auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if err != nil || report.unprovenGraphAddRows != 1 || report.evidencedGraphAddRows != 1 || report.lastLocalSeq != 3 {
		t.Fatalf("Add evidence audit = %+v, %v", report, err)
	}
	path = writeReceiptWALAuditEntries(t, receiptEntry, auditGraphAddEntry(1))
	report, err = auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if !errors.Is(err, errReceiptWALUnion) || !reflect.DeepEqual(report, receiptWALDecisionAudit{}) {
		t.Fatalf("old Add audit = %+v, %v; want no certified partial report", report, err)
	}
	// A zero-accepted sidecar proves a local no-op, without authorizing
	// serving replay or receipt admission.
	path = writeReceiptWALAuditEntries(t, receiptEntry, auditGraphAddEffectEntry(t, 1, false))
	report, err = auditReceiptDecisionsFromFileWAL(path, config, time.Now())
	if err != nil || report.evidencedGraphAddRows != 1 || report.unprovenGraphAddRows != 0 || report.lastLocalSeq != 2 {
		t.Fatalf("zero-accepted Add audit = %+v, %v", report, err)
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
				graph := auditGraphPutEffectEntry(t, 1, graphcache.PutOutcomeAppliedAndLive)
				graph.Op.(*graphPutEffectEnvelope).Mutation.Hlc.WallNs = later.Add(-time.Millisecond).UnixNano()
				graph.HLC = receiptWALUnionGraphHLC(graph.Op.(*graphPutEffectEnvelope).Mutation)
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
