package client

// MutationReply is either a disclosed effect or a handled request whose
// effect is undisclosed. The undisclosed form never contains a fabricated
// primitive, outcome vector, or count.
type MutationReply[T any] struct {
	effect      T
	known       bool
	undisclosed bool
}

// Effect returns the disclosed effect, or the zero value and false when the
// request was handled without disclosing its effect.
func (r MutationReply[T]) Effect() (T, bool) { return r.effect, r.known }

func (r MutationReply[T]) AcceptedUndisclosed() bool { return r.undisclosed }

// MutationReplyFrom adapts a primitive-returning SDK facade. Callers can pass
// its two return values directly, for example:
//
//	reply, err := client.MutationReplyFrom(db.AddEdge(ctx, tail, head, weight, ttl))
//
// Only the dedicated complete-call acknowledgement becomes success. A later
// chunk failure or any other error remains a failure requiring reconciliation.
func MutationReplyFrom[T any](effect T, err error) (MutationReply[T], error) {
	if _, accepted := err.(*MutationAcceptance); accepted {
		return MutationReply[T]{undisclosed: true}, nil
	}
	if err != nil {
		return MutationReply[T]{}, err
	}
	return MutationReply[T]{effect: effect, known: true}, nil
}
