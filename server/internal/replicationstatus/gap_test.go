package replicationstatus

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func TestIsTransientGap(t *testing.T) {
	for _, reason := range []string{ReasonSubscriberStreamClosed, ReasonPublicationFault} {
		t.Run(reason, func(t *testing.T) {
			err := TransientGap(reason, errors.New("gapped"))
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("gap status = %v", err)
			}
			transient, classifyErr := IsTransientGap(err)
			if classifyErr != nil || !transient {
				t.Fatalf("classify %v = (%t, %v), want transient", err, transient, classifyErr)
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"retained history", connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped: missing origin"))},
		{"unknown reason", TransientGap("UNKNOWN_REASON", errors.New("gapped"))},
		{"other code", connect.NewError(connect.CodeUnavailable, errors.New("peer unavailable"))},
		{"other domain", func() error {
			detail, err := connect.NewErrorDetail(&errdetails.ErrorInfo{
				Reason: ReasonSubscriberStreamClosed, Domain: "other.example",
			})
			if err != nil {
				t.Fatal(err)
			}
			gap := connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped"))
			gap.AddDetail(detail)
			return gap
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transient, err := IsTransientGap(tc.err)
			if err != nil || transient {
				t.Fatalf("classify %v = (%t, %v), want non-transient", tc.err, transient, err)
			}
		})
	}
}
