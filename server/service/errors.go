// Package service: errors.go owns the small helpers the service-layer
// RPC methods use to surface failures as native connect.Error values.
//
// Service code returns these errors directly; the Connect-Go handlers
// wrap them once via connect.NewError up the stack. The error model is
// Connect's connect.CodeX enum end-to-end — no
// google.golang.org/grpc/{codes,status} dependency.
package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
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

// traversalToConnect adds stable resource-safety errors on top of the shared
// context mapping. A PPR/community work budget exhaustion is deliberately a
// RESOURCE_EXHAUSTED failure, never a successful-but-truncated graph.
func traversalToConnect(err error) error {
	if errors.Is(err, graphcache.ErrPPRWorkBudgetExceeded) {
		return connect.NewError(connect.CodeResourceExhausted, err)
	}
	return ctxToConnect(err)
}
