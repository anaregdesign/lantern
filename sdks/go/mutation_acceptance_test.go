package client

import (
	"errors"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func TestMutationAcceptanceParsingAndRetryClassification(t *testing.T) {
	for _, response := range []proto.Message{
		&pb.AddEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}},
		&pb.CreateEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}},
		&pb.PutEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}},
		&pb.DeleteEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}},
		&pb.DeleteEdgeContributionsResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}},
		&pb.DeleteEdgesByPrefixResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}},
	} {
		err := mutationAcceptanceFromProto(response)
		var accepted *MutationAcceptance
		if !errors.As(err, &accepted) {
			t.Fatalf("%T: missing typed acceptance: %v", response, err)
		}
		for _, failure := range []error{ErrUnavailable, ErrResourceExhausted, ErrFailedPrecondition, ErrInvalidArgument, ErrUnauthenticated} {
			if errors.Is(err, failure) {
				t.Fatal("acceptance classified as RPC failure", failure)
			}
		}
	}
	valid := &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}
	unknown := proto.Clone(valid).(*pb.MutationAcceptance)
	unknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
	outerUnknown := &pb.AddEdgesResponse{Acceptance: valid}
	outerUnknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
	for _, response := range []proto.Message{
		nil, (*pb.AddEdgesResponse)(nil),
		&pb.AddEdgesResponse{Acceptance: &pb.MutationAcceptance{}},
		&pb.AddEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind(99)}},
		&pb.AddEdgesResponse{Acceptance: valid, Written: 1},
		&pb.AddEdgesResponse{Acceptance: valid, EffectiveWeights: []float32{0}},
		&pb.DeleteEdgesResponse{Acceptance: valid, Existed: []bool{false}},
		&pb.PutEdgesResponse{Acceptance: valid, Outcomes: []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_EXPIRED}},
		&pb.AddEdgesResponse{Acceptance: unknown}, outerUnknown,
	} {
		if err := mutationAcceptanceFromProto(response); !errors.Is(err, ErrMutationProtocol) {
			t.Fatalf("%T: malformed acceptance accepted: %v", response, err)
		}
	}
	if err := mutationAcceptanceFromProto(&pb.AddEdgesResponse{Written: 1, EffectiveWeights: []float32{7}}); err != nil {
		t.Fatal("ordinary effect rejected", err)
	}
}
