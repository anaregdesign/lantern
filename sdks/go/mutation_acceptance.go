package client

import (
	"errors"
	"fmt"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// MutationAcceptance acknowledges that the complete logical request was
// handled while its effects remain undisclosed. It is not an RPC failure,
// permission rejection, confirmation of a change, or permission to retry.
// Primitive-returning facades use this signal instead of inventing an effect;
// MutationReplyFrom converts it to a typed successful acknowledgement.
type MutationAcceptance struct{}

func (*MutationAcceptance) Error() string {
	return "client: mutation handled; effect undisclosed"
}

// ErrMutationProtocol identifies an invalid mutation acknowledgement. Neither
// this error nor MutationAcceptance matches a transport retry sentinel.
var ErrMutationProtocol = errors.New("client: invalid mutation response")

func mutationAcceptanceFromProto(response proto.Message) error {
	if response == nil || !response.ProtoReflect().IsValid() {
		return fmt.Errorf("%w: nil response", ErrMutationProtocol)
	}
	message := response.ProtoReflect()
	field := message.Descriptor().Fields().ByName("acceptance")
	if field == nil || !message.Has(field) {
		return nil
	}
	acceptance, valid := message.Get(field).Message().Interface().(*pb.MutationAcceptance)
	if !valid || acceptance.GetKind() != pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED || len(acceptance.ProtoReflect().GetUnknown()) != 0 || len(message.GetUnknown()) != 0 {
		return fmt.Errorf("%w: unknown acceptance", ErrMutationProtocol)
	}
	mixed := false
	message.Range(func(candidate protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		mixed = mixed || candidate != field
		return !mixed
	})
	if mixed {
		return fmt.Errorf("%w: acceptance carried detailed effects", ErrMutationProtocol)
	}
	return &MutationAcceptance{}
}
