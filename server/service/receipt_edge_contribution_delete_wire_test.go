package service

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestReceiptEdgeContributionDeleteWirePreservesFalseResultAndIdentity(t *testing.T) {
	_, envelope := receiptEdgeContributionDeleteCodecFixture(t)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	call := wire.GetOp().GetReplicatedReceiptEdgeContributionDelete()
	if call == nil || wire.GetOp().GetDeleteEdgeContributions() != nil ||
		len(call.GetItems()) != 3 {
		t.Fatalf("receipt-bearing arm was downgraded: %+v", wire)
	}
	for i, item := range call.GetItems() {
		if !bytes.Equal(item.GetKey().GetContribId(), envelope.OriginalKeys[i].ContribID[:]) ||
			!item.GetCausallyAccepted() {
			t.Fatalf("wire item %d lost target ID or accepted bit: %+v", i, item)
		}
		result, ok := item.GetReceipt().GetOriginalResult().GetResult().(*pb.ReceiptResult_DeleteEdgeContributionExisted)
		if !ok || result.DeleteEdgeContributionExisted != (i == 0) {
			t.Fatalf("wire item %d lost original-result presence (including false): %+v", i, item)
		}
	}
	accepted, err := acceptedReceiptEdgeContributionDeleteKeys(wire)
	if err != nil || len(accepted) != 3 || accepted[0].GetHead() != "present" ||
		accepted[1].GetHead() != "absent" {
		t.Fatalf("accepted identity-only keys = %+v, %v", accepted, err)
	}
	var frames []*pb.SubscribeResponse
	if err := projectMutationIdentities(wire, func(frame *pb.SubscribeResponse) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 ||
		frames[0].GetIdentityChunk().GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE_CONTRIBUTION ||
		len(frames[0].GetIdentityChunk().GetEdgeKeys()) != 3 ||
		frames[0].GetIdentityChunk().GetEdgeKeys()[0].GetHead() != "present" {
		t.Fatalf("identity-only contribution Delete projection = %+v", frames)
	}
	envelope.OriginalKeys[0].ContribID[0] ^= 1
	envelope.Receipts[1].Result[0] = 1
	if call.GetItems()[0].GetKey().GetContribId()[0] != 0x80 ||
		call.GetItems()[1].GetReceipt().GetOriginalResult().GetDeleteEdgeContributionExisted() {
		t.Fatal("wire aliases mutable WAL envelope")
	}
}

func TestReceiptEdgeContributionDeleteWireReceiptOnlyAndTampering(t *testing.T) {
	_, original := receiptEdgeContributionDeleteCodecFixture(t)
	receiptOnly := cloneReceiptEdgeContributionDeleteCodecEnvelope(original)
	receiptOnly.Accepted = nil
	receiptOnly.Mutation = receiptEdgeContributionDeleteWALMutation(receiptOnly)
	wire, err := receiptOnly.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	var frames []*pb.SubscribeResponse
	if err := projectMutationIdentities(wire, func(frame *pb.SubscribeResponse) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 ||
		frames[0].GetIdentityChunk().GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY ||
		!frames[0].GetIdentityChunk().GetIsLast() ||
		len(frames[0].GetIdentityChunk().GetEdgeKeys()) != 0 ||
		!wire.GetOp().GetReplicatedReceiptEdgeContributionDelete().GetItems()[0].
			GetReceipt().GetOriginalResult().GetDeleteEdgeContributionExisted() {
		t.Fatalf("receipt-only cursor or sender result = %+v, wire=%+v", frames, wire)
	}
	full, err := original.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"missing epoch", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().DeploymentEpoch = nil
		}},
		{"missing result", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[1].Receipt.OriginalResult = nil
		}},
		{"wrong oneof result", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[1].Receipt.OriginalResult =
				&pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: false}}
		}},
		{"zero target", func(m *pb.Mutation) {
			clear(m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].Key.ContribId)
		}},
		{"short target", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].Key.ContribId =
				[]byte{1}
		}},
		{"target digest conflict", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].Key.ContribId[0] ^= 1
		}},
		{"index drift", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].Receipt.ItemIndex = 1
		}},
		{"nil key", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].Key = nil
		}},
		{"invalid UTF-8", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].Key.Tail = "\xff"
		}},
		{"outer expiration", func(m *pb.Mutation) {
			m.TombstoneExpiration = timestamppb.New(time.Now().Add(time.Hour))
		}},
		{"missing inner expiration", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().TombstoneExpiration = nil
		}},
		{"unknown nested fields", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0].Key.ProtoReflect().
				SetUnknown([]byte{0xf8, 0x07, 0x01})
		}},
		{"typed nil arm", func(m *pb.Mutation) {
			m.Op.Op = &pb.MutationOp_ReplicatedReceiptEdgeContributionDelete{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := proto.Clone(full).(*pb.Mutation)
			tc.mutate(bad)
			if _, err := decodeReceiptEdgeContributionDeleteMutation(bad); err == nil {
				t.Fatal("malformed receipt wire decoded")
			}
			if err := projectMutationIdentities(bad, func(*pb.SubscribeResponse) error {
				t.Fatal("malformed receipt wire advanced identity cursor")
				return nil
			}); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("malformed identity projection = %v, want Internal", err)
			}
		})
	}
}

func TestReceiptEdgeContributionDeleteWireOwnsDecodedStateAndReceiverIntent(t *testing.T) {
	_, original := receiptEdgeContributionDeleteCodecFixture(t)
	wire, err := original.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptEdgeContributionDeleteMutation(wire)
	if err != nil {
		t.Fatal(err)
	}
	wire.Origin[0] ^= 1
	item := wire.GetOp().GetReplicatedReceiptEdgeContributionDelete().Items[0]
	item.Key.ContribId[0] ^= 1
	item.Receipt.OperationId[0] ^= 1
	item.Receipt.OriginalResult = &pb.ReceiptResult{
		Result: &pb.ReceiptResult_DeleteEdgeContributionExisted{},
	}
	if decoded.Origin != original.Origin ||
		decoded.OriginalKeys[0] != original.OriginalKeys[0] ||
		!reflect.DeepEqual(decoded.Receipts[0].Result, original.Receipts[0].Result) {
		t.Fatal("decoded envelope aliases caller-owned wire")
	}
	other := cloneReceiptEdgeContributionDeleteCodecEnvelope(decoded)
	other.Accepted = nil
	other.Mutation = receiptEdgeContributionDeleteWALMutation(other)
	if !sameReceiptEdgeContributionDeleteIntent(decoded, other) {
		t.Fatal("receiver-local accepted projection changed immutable intent")
	}
	other.OriginalKeys[0].ContribID = graphcache.ContribID{0xaa}
	if sameReceiptEdgeContributionDeleteIntent(decoded, other) {
		t.Fatal("a different target ID matched immutable receipt intent")
	}
}
