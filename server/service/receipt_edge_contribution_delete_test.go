package service

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type receiptContributionDeleteFixture struct {
	receiptEdgeDeleteFixture
	contribution *edgeContributionDeleteReceiptCoordinator
}

func newReceiptContributionDeleteFixture(
	t *testing.T, wal mutationlog.WAL, node hlc.NodeID,
) receiptContributionDeleteFixture {
	t.Helper()
	f := newReceiptEdgeDeleteFixtureWithLimits(t, wal, node, 32, 1<<20)
	coordinator, err := newEdgeContributionDeleteReceiptCoordinator(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	return receiptContributionDeleteFixture{receiptEdgeDeleteFixture: f, contribution: coordinator}
}

func receiptContributionDeleteCall(
	t *testing.T, epoch mutationreceipt.Epoch, keys ...graphcache.EdgeContributionKey[string],
) receiptEdgeContributionDeleteCall {
	t.Helper()
	call := receiptEdgeContributionDeleteCall{
		Group: mutationreceipt.GroupID{0x7d},
		Items: make([]receiptEdgeContributionDeleteItem, len(keys)),
	}
	issued := time.Now().Add(-time.Second)
	for i, key := range keys {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		call.Items[i] = receiptEdgeContributionDeleteItem{
			ID: id, Tail: key.Tail, Head: key.Head, ContribID: key.ContribID,
		}
	}
	return call
}

func TestReceiptEdgeContributionDeleteOriginalOutcomesAndRetry(t *testing.T) {
	f := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x41})
	expiration := time.Now().Add(time.Hour)
	target := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "head", ContribID: graphcache.ContribID{0x80},
	}
	survivor := target
	survivor.ContribID = graphcache.ContribID{0x81}
	absent := target
	absent.ContribID = graphcache.ContribID{0x82}
	if !f.cache.AddEdgeWithExpirationContrib("tail", "head", 2, expiration, target.ContribID) ||
		!f.cache.AddEdgeWithExpirationContrib("tail", "head", 3, expiration, survivor.ContribID) {
		t.Fatal("initial contributions were not admitted")
	}
	call := receiptContributionDeleteCall(t, f.epoch, target, target, absent)
	resp, err := f.contribution.Commit(context.Background(), call)
	if err != nil || resp.GetDeleted() != 1 ||
		!reflect.DeepEqual(resp.GetExisted(), []bool{true, false, false}) {
		t.Fatalf("original outcomes = %+v, %v", resp, err)
	}
	if weight, live := f.cache.GetWeight("tail", "head"); !live || weight != 3 {
		t.Fatalf("remaining Add row = (%v, %v), want weight 3", weight, live)
	}
	tombstones := f.cache.SnapshotReplication().Tombstones.EdgeContributions
	if len(tombstones) != 2 {
		t.Fatalf("absent/duplicate accepted Delete tombstones = %+v", tombstones)
	}
	entry := f.log.RetainedEntries()[0]
	envelope, ok := entry.Op.(*edgeContributionDeleteReceiptEnvelope)
	if !ok || len(envelope.Accepted) != 3 || len(envelope.Receipts) != 3 {
		t.Fatalf("committed receipt WAL envelope = %T %+v", entry.Op, envelope)
	}
	for i, want := range []byte{1, 0, 0} {
		if !reflect.DeepEqual(envelope.Receipts[i].Result, []byte{want}) ||
			envelope.Receipts[i].Kind != mutationreceipt.DeleteEdgeContribution ||
			envelope.Receipts[i].HasContrib {
			t.Fatalf("receipt %d = %+v, want result %d without Add reverse binding",
				i, envelope.Receipts[i], want)
		}
	}
	again, err := f.contribution.Commit(context.Background(), call)
	if err != nil || !reflect.DeepEqual(again, resp) ||
		f.log.Len() != 1 || f.service.LocalSeq(f.service.clock.NodeID()) != 1 {
		t.Fatalf("duplicate retried result = %+v, %v; log=%d seq=%d",
			again, err, f.log.Len(), f.service.LocalSeq(f.service.clock.NodeID()))
	}
	changed := call
	changed.Items = append([]receiptEdgeContributionDeleteItem(nil), call.Items...)
	changed.Items[0].ContribID = graphcache.ContribID{0x83}
	if _, err := f.contribution.Commit(context.Background(), changed); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("same operation ID with a different target = %v", err)
	}
}

func TestReceiptEdgeContributionDeletePublicStatusRetainsFalseAndIntent(t *testing.T) {
	runtime, service, _ := newActivatedReceiptService(t, 8)
	target := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "head", ContribID: graphcache.ContribID{0x80},
	}
	receiptContext := publicReceiptContext(t, runtime, 0x70, 1)
	request := &pb.DeleteEdgeContributionsRequest{
		Contributions: []*pb.EdgeContributionKey{{
			Tail: target.Tail, Head: target.Head, ContribId: target.ContribID[:],
		}},
		ReceiptContext: receiptContext,
	}
	response, err := service.DeleteEdgeContributions(context.Background(), request)
	if err != nil || response.GetDeleted() != 0 ||
		!reflect.DeepEqual(response.GetExisted(), []bool{false}) {
		t.Fatalf("public absent-target Delete = %+v, %v", response, err)
	}
	status, err := service.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: receiptContext.GetOperationIds()[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := status.GetStatus().GetReceipt()
	result, present := receipt.GetOriginalResult().GetResult().(*pb.ReceiptResult_DeleteEdgeContributionExisted)
	digest := edgeContributionDeleteDigest(target)
	if status.GetStatus().GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		!present || result.DeleteEdgeContributionExisted ||
		!bytes.Equal(receipt.GetIntentSha256(), digest[:]) {
		t.Fatalf("singular receipt status lost false presence or full target intent: %+v", status)
	}
	id, err := mutationreceipt.DecodeID(receiptContext.GetOperationIds()[0])
	if err != nil {
		t.Fatal(err)
	}
	storedStatus, stored, err := runtime.receipt.store.Lookup(id, time.Now())
	if err != nil || storedStatus != mutationreceipt.Confirmed ||
		stored.Kind != mutationreceipt.DeleteEdgeContribution || stored.HasContrib ||
		stored.ContribID != (mutationreceipt.ContribID{}) {
		t.Fatalf("target was incorrectly bound as an Add receipt: %v, %+v, %v", storedStatus, stored, err)
	}
	other := graphcache.ContribID{0x81}
	_, err = service.DeleteEdgeContributions(context.Background(), &pb.DeleteEdgeContributionsRequest{
		Contributions: []*pb.EdgeContributionKey{{
			Tail: target.Tail, Head: target.Head, ContribId: other[:],
		}},
		ReceiptContext: receiptContext,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("same operation ID with a different target = %v", err)
	}
}

func TestReceiptEdgeContributionDeleteWALAbortAndFailStop(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wal        mutationlog.WAL
		indefinite bool
	}{
		{"definite abort", receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected definite abort")}
		}), false},
		{"indeterminate outcome", receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
			return errors.New("lost WAL acknowledgement")
		}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReceiptContributionDeleteFixture(t, tc.wal, hlc.NodeID{0x41})
			target := graphcache.EdgeContributionKey[string]{
				Tail: "tail", Head: "head", ContribID: graphcache.ContribID{0x80},
			}
			if !f.cache.AddEdgeWithExpirationContrib("tail", "head", 2,
				time.Now().Add(time.Hour), target.ContribID) {
				t.Fatal("initial Add rejected")
			}
			call := receiptContributionDeleteCall(t, f.epoch, target)
			if _, err := f.contribution.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
				t.Fatalf("WAL failure = %v, want Unavailable", err)
			}
			if weight, live := f.cache.GetWeight("tail", "head"); !live || weight != 2 {
				t.Fatalf("aborted graph = (%v,%v), want original row", weight, live)
			}
			if got := f.cache.SnapshotReplication().Tombstones.EdgeContributions; len(got) != 0 {
				t.Fatalf("aborted D4 tombstone escaped: %+v", got)
			}
			if f.log.Len() != 0 || f.service.LocalSeq(f.service.clock.NodeID()) != 0 {
				t.Fatalf("aborted log/origin = (%d,%d)", f.log.Len(), f.service.LocalSeq(f.service.clock.NodeID()))
			}
			if f.service.receiptCommitFaulted != tc.indefinite {
				t.Fatalf("fail-stop = %v, want %v", f.service.receiptCommitFaulted, tc.indefinite)
			}
			status, _, err := f.coordinator.store.Lookup(call.Items[0].ID, time.Now())
			if err != nil || status != mutationreceipt.NotYetObserved {
				t.Fatalf("aborted receipt = %v, %v", status, err)
			}
		})
	}
}

func TestReceiptEdgeContributionDeleteSnapshotKeepsFalseAndTombstone(t *testing.T) {
	f := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x41})
	target := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "absent", ContribID: graphcache.ContribID{0x80},
	}
	call := receiptContributionDeleteCall(t, f.epoch, target)
	if resp, err := f.contribution.Commit(context.Background(), call); err != nil ||
		!reflect.DeepEqual(resp.GetExisted(), []bool{false}) {
		t.Fatalf("accepted absent-target Delete = %+v, %v", resp, err)
	}
	policy := receiptCapturePolicy(f.epoch)
	source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.Capture(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	var row *pb.SnapshotReceipt
	var tombstone *pb.SnapshotEdgeContributionTombstone
	for _, frame := range frames {
		if frame.GetReceipt() != nil {
			row = frame.GetReceipt()
		}
		if frame.GetEdgeContributionTombstone() != nil {
			tombstone = frame.GetEdgeContributionTombstone()
		}
	}
	if row == nil ||
		row.GetKind() != pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_DELETE_EDGE_CONTRIBUTION ||
		!bytes.Equal(row.GetOriginalResult(), []byte{0}) || row.GetContribution() != nil ||
		tombstone == nil || tombstone.GetTail() != target.Tail ||
		tombstone.GetHead() != target.Head ||
		!bytes.Equal(tombstone.GetContribId(), target.ContribID[:]) {
		t.Fatalf("receipt Snapshot lost false result or D4 target: row=%+v tombstone=%+v", row, tombstone)
	}
	retiredConfig, _, err := retiredCatalogConfig(policy, capture.Receipts.ClockHighWaterMillis)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReceiptSnapshotFrames(frames, policy, retiredConfig)
	if err != nil || len(decoded.Receipts.Receipts) != 1 ||
		!reflect.DeepEqual(decoded.Receipts.Receipts[0].Result, []byte{0}) {
		t.Fatalf("contribution Delete Snapshot roundtrip = %+v, %v", decoded.Receipts, err)
	}
	row.OriginalResult = nil
	if _, err := DecodeReceiptSnapshotFrames(frames, policy, retiredConfig); err == nil {
		t.Fatal("Snapshot with absent false result decoded")
	}
}

func TestReceiptEdgeContributionDeleteReplicationKeepsOriginalResultAndTombstone(t *testing.T) {
	origin := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x41})
	follower := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x51})
	target := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "head", ContribID: graphcache.ContribID{0x80},
	}
	expiration := time.Now().Add(time.Hour)
	if !origin.cache.AddEdgeWithExpirationContrib("tail", "head", 2, expiration, target.ContribID) {
		t.Fatal("origin Add rejected")
	}
	call := receiptContributionDeleteCall(t, origin.epoch, target)
	if resp, err := origin.contribution.Commit(context.Background(), call); err != nil || !resp.GetExisted()[0] {
		t.Fatalf("origin accepted Delete = %+v, %v", resp, err)
	}
	wire, err := origin.log.RetainedEntries()[0].Op.(*edgeContributionDeleteReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	if err := follower.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatalf("receiver-local absent target = %v", err)
	}
	entry := follower.log.RetainedEntries()[0]
	relayed, ok := entry.Op.(*edgeContributionDeleteReceiptEnvelope)
	if !ok || len(relayed.Accepted) != 1 ||
		!reflect.DeepEqual(relayed.Receipts[0].Result, []byte{1}) {
		t.Fatalf("follower lost origin's true original result: %T %+v", entry.Op, relayed)
	}
	status, receipt, err := follower.coordinator.store.Lookup(call.Items[0].ID, time.Now())
	if err != nil || status != mutationreceipt.Confirmed ||
		!reflect.DeepEqual(receipt.Result, []byte{1}) {
		t.Fatalf("follower original receipt = %v, %+v, %v", status, receipt, err)
	}
	if len(follower.cache.SnapshotReplication().Tombstones.EdgeContributions) != 1 {
		t.Fatal("accepted absent deletion did not install a D4 tombstone")
	}
	earlier := hlc.Timestamp{WallNs: wire.GetHlc().GetWallNs() - 1, NodeID: origin.service.clock.NodeID()}
	if follower.cache.AddEdgeWithExpirationContribHLC("tail", "head", 2,
		expiration, target.ContribID, earlier) {
		t.Fatal("delayed Add resurrected a deleted contribution")
	}
	if err := follower.service.ApplyMutation(context.Background(), wire); err != nil ||
		follower.log.Len() != 1 {
		t.Fatalf("duplicate peer mutation = %v, log=%d", err, follower.log.Len())
	}
}

func TestReceiptEdgeContributionDeleteReplicatedNoOpReprojectsLocally(t *testing.T) {
	origin := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x41})
	follower := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x51})
	target := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "missing", ContribID: graphcache.ContribID{0x80},
	}
	call := receiptContributionDeleteCall(t, origin.epoch, target)
	if response, err := origin.contribution.Commit(context.Background(), call); err != nil ||
		!reflect.DeepEqual(response.GetExisted(), []bool{false}) {
		t.Fatalf("origin absent-target result = %+v, %v", response, err)
	}
	wire, err := origin.log.RetainedEntries()[0].Op.(*edgeContributionDeleteReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	wire.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].CausallyAccepted = false
	if err := follower.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatalf("receiver-local contribution Delete with sender rejection = %v", err)
	}
	relay, ok := follower.log.RetainedEntries()[0].Op.(*edgeContributionDeleteReceiptEnvelope)
	if !ok || len(relay.Accepted) != 1 || relay.Accepted[0].Key != target ||
		!reflect.DeepEqual(relay.Receipts[0].Result, []byte{0}) {
		t.Fatalf("follower lost original false or receiver-local acceptance: %T %+v", relay, relay)
	}
	if len(follower.cache.SnapshotReplication().Tombstones.EdgeContributions) != 1 {
		t.Fatal("receiver-local accepted absent Delete did not install a D4 tombstone")
	}
}

func TestReceiptEdgeContributionDeleteRejectsMissingIdentityAndResult(t *testing.T) {
	f := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x41})
	valid := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "head", ContribID: graphcache.ContribID{0x80},
	}
	for _, tc := range []struct {
		name string
		key  graphcache.EdgeContributionKey[string]
	}{
		{"zero ContribID", graphcache.EdgeContributionKey[string]{Tail: "tail", Head: "head"}},
		{"empty endpoint", graphcache.EdgeContributionKey[string]{Tail: "", Head: "head", ContribID: valid.ContribID}},
		{"invalid UTF-8", graphcache.EdgeContributionKey[string]{Tail: "\xff", Head: "head", ContribID: valid.ContribID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := receiptContributionDeleteCall(t, f.epoch, tc.key)
			if _, err := f.contribution.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeInvalidArgument ||
				f.log.Len() != 0 {
				t.Fatalf("malformed identity = %v, log=%d", err, f.log.Len())
			}
		})
	}
	if _, err := receiptEdgeContributionDeleteResponse([]mutationreceipt.Receipt{{
		Intent: mutationreceipt.Intent{Kind: mutationreceipt.DeleteEdgeContribution},
		Result: nil,
	}}); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("missing original-result presence = %v", err)
	}
}
