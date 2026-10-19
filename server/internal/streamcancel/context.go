// Package streamcancel carries a request owner's interruption capability to
// synchronous stream senders. It can withdraw a request, never extend admission.
package streamcancel

import "context"

type requestKey struct{}

// WithInterrupt preserves ctx's deadline and cancellation and attaches the
// request owner's existing cancellation function.
func WithInterrupt(ctx context.Context, interrupt context.CancelFunc) context.Context {
	return context.WithValue(ctx, requestKey{}, interrupt)
}

// Interrupt asks the request owner to stop blocked I/O. False means this
// transport did not provide an interruption capability.
func Interrupt(ctx context.Context) bool {
	interrupt, _ := ctx.Value(requestKey{}).(context.CancelFunc)
	if interrupt == nil {
		return false
	}
	interrupt()
	return true
}
