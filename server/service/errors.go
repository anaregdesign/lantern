// Package service: errors.go owns the small helpers the service-layer
// RPC methods use to surface failures as native connect.Error values.
//
// Service code returns these errors directly; the Connect-Go handlers
// wrap them once via connect.NewError up the stack. There is no
// google.golang.org/grpc/{codes,status} dependency: every code that
// used to be codes.X is now connect.CodeX.
package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
)

// ctxToConnect translates ctx.Err() (or any wrapped form thereof) into
// the Connect code the protocol assigns to it. Non-context errors fall
// through unchanged.
//
//   - context.Canceled        → connect.CodeCanceled
//   - context.DeadlineExceeded → connect.CodeDeadlineExceeded
//
// Returning the original err as the underlying error preserves
// errors.Is(err, context.Canceled) for callers that need it.
func ctxToConnect(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	default:
		return err
	}
}
