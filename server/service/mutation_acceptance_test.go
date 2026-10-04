package service

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestMutationAcceptanceResponseDiscardsEveryEffectAndPreservesOriginal(t *testing.T) {
	for _, original := range []proto.Message{
		&pb.AddEdgeResponse{EffectiveWeight: 7},
		&pb.AddEdgesResponse{Written: 2, EffectiveWeights: []float32{7, 11}},
		&pb.PutEdgeResponse{Outcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE},
		&pb.PutEdgesResponse{Outcomes: []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_EXPIRED}},
		&pb.CreateEdgeResponse{Outcome: pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EDGE_EXISTS},
		&pb.CreateEdgesResponse{Outcomes: []pb.CreateEdgeOutcome{pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_ENDPOINT_NOT_LIVE}},
		&pb.DeleteEdgeResponse{Existed: true},
		&pb.DeleteEdgesResponse{Deleted: 1, Existed: []bool{true, false}},
		&pb.DeleteEdgeContributionResponse{Existed: true},
		&pb.DeleteEdgeContributionsResponse{Deleted: 1, Existed: []bool{false, true}},
		&pb.DeleteEdgesByPrefixResponse{Deleted: 19},
	} {
		t.Run(string(original.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			original.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
			before, err := proto.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			result, err := mutationAcceptanceResponse(original)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ProtoReflect().GetUnknown()) != 0 {
				t.Fatal("unknown result fields escaped")
			}
			fields := 0
			result.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
				fields++
				if field.Name() != "acceptance" || value.Message().Interface().(*pb.MutationAcceptance).Kind != pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED {
					t.Fatalf("private result field escaped: %s", field.Name())
				}
				return true
			})
			if fields != 1 {
				t.Fatal("acceptance must be the only populated response field")
			}
			after, err := proto.Marshal(original)
			if err != nil || string(before) != string(after) {
				t.Fatal("authoritative original result was modified", err)
			}
		})
	}
	for _, invalid := range []proto.Message{nil, (*pb.AddEdgesResponse)(nil), &pb.GetEdgesResponse{}} {
		if _, err := mutationAcceptanceResponse(invalid); connect.CodeOf(err) != connect.CodeInternal {
			t.Fatal("invalid response accepted", err)
		}
	}
}

func TestBlindMutationDisclosureUsesPolicyAndRequestOnly(t *testing.T) {
	clock, contexts := dataAccessFixture(t, []security.PermissionRule{
		dataAccessRule("tail-read", security.Allow, security.VertexRead, "tails:"),
		dataAccessRule("public-head-read", security.Allow, security.VertexRead, "heads:public:"),
		dataAccessRule("head-write", security.Allow, security.VertexWrite, "heads:"),
	})
	admission, _ := security.AdmissionFromContext(contexts("reader", clock()))
	for _, tc := range []struct {
		request proto.Message
		want    bool
	}{
		{&pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "tails:a", Head: "heads:public:a"}}}, false},
		{&pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "tails:a", Head: "heads:public:a"}, {Tail: "tails:a", Head: "heads:private:a"}}}, true},
		{&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "tails:a", Head: "heads:private:a"}}, true},
		{&pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "tails:a", Head: "heads:private:a"}}}, true},
		{&pb.DeleteEdgeRequest{Tail: "tails:a", Head: "heads:private:a"}, true},
		{&pb.DeleteEdgeContributionsRequest{Contributions: []*pb.EdgeContributionKey{{Tail: "tails:a", Head: "heads:private:a"}}}, true},
		{&pb.DeleteEdgesByPrefixRequest{TailPrefix: "tails:", HeadPrefix: "heads:public:"}, false},
		{&pb.DeleteEdgesByPrefixRequest{TailPrefix: "tails:", HeadPrefix: "heads:"}, true},
	} {
		if got := blindMutationRequired(admission, tc.request); got != tc.want {
			t.Fatalf("%T disclosure = %v, want %v", tc.request, got, tc.want)
		}
		if blindMutationRequired(nil, tc.request) {
			t.Fatal("OFF behavior became undisclosed")
		}
	}
}

func TestBlindReceiptDispositionDoesNotMaskInputAuthorityOrDurabilityFailures(t *testing.T) {
	for _, err := range []error{mutationreceipt.ErrIntentConflict, mutationreceipt.ErrContributionConflict, mutationreceipt.ErrPartialEnvelope, mutationreceipt.ErrNoLongerProvable, mutationreceipt.ErrNotFresh} {
		if !blindReceiptDisposition(fmt.Errorf("receipt: %w", err)) || !blindReceiptDisposition(connect.NewError(connect.CodeFailedPrecondition, err)) {
			t.Fatal("private receipt disposition not projected", err)
		}
	}
	for _, err := range []error{nil, mutationreceipt.ErrInvalidID, mutationreceipt.ErrInvalidClock, security.ErrPermissionDenied, security.ErrAuthorityUnavailable, errors.New("WAL commit failed"), connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))} {
		if blindReceiptDisposition(err) {
			t.Fatal("public rejection or durability failure masked", err)
		}
	}
}
