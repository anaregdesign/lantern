package client

import (
	"errors"
	"testing"
)

func TestMutationReplyDoesNotInventAnEffectOrHidePartialFailure(t *testing.T) {
	for _, value := range []bool{true, false} {
		reply, err := MutationReplyFrom(value, nil)
		got, known := reply.Effect()
		if err != nil || !known || got != value || reply.AcceptedUndisclosed() {
			t.Fatal("known effect drift", reply, err)
		}
	}
	reply, err := MutationReplyFrom(true, error(&MutationAcceptance{}))
	if err != nil || !reply.AcceptedUndisclosed() {
		t.Fatal("complete acceptance was not typed", err)
	}
	if value, known := reply.Effect(); value || known {
		t.Fatal("undisclosed result invented an effect")
	}
	if _, known := (MutationReply[bool]{}).Effect(); known {
		t.Fatal("zero-value reply claimed a disclosed effect")
	}
	for _, failure := range []error{ErrUnavailable, &BatchError{Written: 2, Err: ErrUnavailable}, &BatchError{Written: 2, Err: &MutationAcceptance{}}} {
		reply, err := MutationReplyFrom(true, failure)
		if !errors.Is(err, failure) || reply.AcceptedUndisclosed() {
			t.Fatal("partial or real failure became acceptance", err)
		}
		if _, known := reply.Effect(); known {
			t.Fatal("failure reply exposed an effect")
		}
	}
}
