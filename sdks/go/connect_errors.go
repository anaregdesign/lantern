package client

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// connectErrToGRPC translates a *connect.Error into the gRPC status.Status
// shape every existing SDK call site already expects (wrapStatus in
// client.go does the final ErrNotFound/ErrInvalidArgument/
// ErrResourceExhausted lifting). The 1:1 code table mirrors the table
// Connect-Go itself uses, so a Connect-side codes.NotFound surfaces as a
// gRPC codes.NotFound, etc.
//
// Errors that already carry gRPC status (e.g. errors produced by the
// in-process connect server handler that calls into existing service
// code returning status.Errorf) round-trip through unchanged because
// errors.As(*connect.Error) reports false on them.
//
// Returning the gRPC status error keeps the public wrapStatus shim AND
// any consumer's `status.Code(err)` switch keep working post-transport
// switch — the SDK consumers do not need to learn the connect.Code API
// until the legacy grpc path is removed in #347 / #342.
func connectErrToGRPC(err error) error {
	if err == nil {
		return nil
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}
	return status.Error(connectCodeToGRPC(cerr.Code()), cerr.Message())
}

// connectCodeToGRPC is the 1:1 mapping between Connect's CodeXxx (which
// match the gRPC names verbatim — Connect was designed to be wire-
// compatible) and gRPC's codes.Code. The unknown fallback preserves
// codes.Unknown semantics for any future Connect code that gRPC has
// not yet learned.
func connectCodeToGRPC(c connect.Code) codes.Code {
	switch c {
	case connect.CodeCanceled:
		return codes.Canceled
	case connect.CodeUnknown:
		return codes.Unknown
	case connect.CodeInvalidArgument:
		return codes.InvalidArgument
	case connect.CodeDeadlineExceeded:
		return codes.DeadlineExceeded
	case connect.CodeNotFound:
		return codes.NotFound
	case connect.CodeAlreadyExists:
		return codes.AlreadyExists
	case connect.CodePermissionDenied:
		return codes.PermissionDenied
	case connect.CodeResourceExhausted:
		return codes.ResourceExhausted
	case connect.CodeFailedPrecondition:
		return codes.FailedPrecondition
	case connect.CodeAborted:
		return codes.Aborted
	case connect.CodeOutOfRange:
		return codes.OutOfRange
	case connect.CodeUnimplemented:
		return codes.Unimplemented
	case connect.CodeInternal:
		return codes.Internal
	case connect.CodeUnavailable:
		return codes.Unavailable
	case connect.CodeDataLoss:
		return codes.DataLoss
	case connect.CodeUnauthenticated:
		return codes.Unauthenticated
	default:
		return codes.Unknown
	}
}
