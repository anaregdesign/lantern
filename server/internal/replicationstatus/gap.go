package replicationstatus

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

const (
	domain                       = "github.com/anaregdesign/lantern"
	ReasonSubscriberStreamClosed = "SUBSCRIBER_STREAM_CLOSED"
	ReasonPublicationFault       = "PUBLICATION_FAULT"
)

// TransientGap preserves the gapped status while telling a peer that this
// responder cannot serve the current stream; only a retained-history gap
// requires a Snapshot of this responder.
func TransientGap(reason string, cause error) error {
	detail, err := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Reason: reason,
		Domain: domain,
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("encode replication gap reason: %w", err))
	}
	gap := connect.NewError(connect.CodeFailedPrecondition, cause)
	gap.AddDetail(detail)
	return gap
}

// IsTransientGap reports only known, locally owned reasons. Unknown or
// unannotated FailedPrecondition errors retain the Snapshot fallback.
func IsTransientGap(err error) (bool, error) {
	var gap *connect.Error
	if !errors.As(err, &gap) || gap.Code() != connect.CodeFailedPrecondition {
		return false, nil
	}
	for _, detail := range gap.Details() {
		if detail.Type() != "google.rpc.ErrorInfo" {
			continue
		}
		value, decodeErr := detail.Value()
		if decodeErr != nil {
			return false, fmt.Errorf("decode replication gap reason: %w", decodeErr)
		}
		info, ok := value.(*errdetails.ErrorInfo)
		if !ok {
			return false, fmt.Errorf("replication gap reason has unexpected type %T", value)
		}
		if info.GetDomain() == domain {
			switch info.GetReason() {
			case ReasonSubscriberStreamClosed, ReasonPublicationFault:
				return true, nil
			}
		}
	}
	return false, nil
}
