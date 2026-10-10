package security

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/anypb"
)

type currentOutputInterceptor struct{ output *CurrentOutput }

func (i currentOutputInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		response, err := next(ctx, req)
		if err == nil && response != nil {
			if _, err = freezeCurrentHeaders(response.Header()); err == nil {
				_, err = freezeCurrentHeaders(response.Trailer())
			}
		}
		if err != nil {
			return nil, i.output.responseError(ctx, err)
		}
		return response, nil
	}
}
func (i currentOutputInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
func (i currentOutputInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		err := next(ctx, currentOutputStream{StreamingHandlerConn: conn})
		if _, metadataErr := freezeCurrentHeaders(conn.ResponseTrailer()); metadataErr != nil {
			err = metadataErr
		}
		if err != nil {
			// An error before the first unit may become a fixed public refusal.
			// After a unit, the old grant remains and the final native check
			// refuses any stale protected terminal status.
			result := i.output.responseError(ctx, err)
			if isCurrentPublicFailure(ctx) {
				clear(conn.ResponseHeader())
				clear(conn.ResponseTrailer())
			}
			return result
		}
		return nil
	}
}

type currentOutputStream struct{ connect.StreamingHandlerConn }

func (c currentOutputStream) Send(value any) error {
	if _, err := freezeCurrentHeaders(c.ResponseHeader()); err != nil {
		return err
	}
	if _, err := freezeCurrentHeaders(c.ResponseTrailer()); err != nil {
		return err
	}
	return c.StreamingHandlerConn.Send(value)
}

func (p *CurrentOutput) responseError(ctx context.Context, err error) error {
	r, requestErr := currentRequest(ctx, p.owner)
	if requestErr != nil {
		return connect.NewError(connect.CodeUnavailable, errCurrentOutput)
	}
	r.mu.Lock()
	grant, started := r.grant, r.started
	r.mu.Unlock()
	_, authErr := r.authorize(ctx, grant)
	var failure *connect.Error
	if !errors.As(err, &failure) {
		failure = connect.NewError(connect.CodeOf(err), errors.New("public RPC request rejected"))
	}
	if authErr != nil {
		if !started {
			bindCurrentFailure(r.writer)
		}
		return connect.NewError(failure.Code(), errors.New("public RPC request rejected"))
	}
	if len(failure.Message()) > 4096 || len(failure.Details()) > 8 {
		return currentEncodingLimit()
	}
	metadata, metadataErr := freezeCurrentHeaders(failure.Meta())
	if metadataErr != nil {
		return currentEncodingLimit()
	}
	result := connect.NewError(failure.Code(), errors.New(failure.Message()))
	for key, values := range metadata {
		result.Meta()[key] = values
	}
	for _, detail := range failure.Details() {
		// The only public service detail producers use boundedErrorDetail,
		// which checks proto.Size before Connect's initial Any allocation.
		// Freeze a new Any to detach Connect's optional mutable pbInner.
		if detail == nil || len(detail.Type()) > 256 {
			return currentEncodingLimit()
		}
		raw := detail.Bytes()
		if len(raw) > 4096 {
			return currentEncodingLimit()
		}
		frozen, e := connect.NewErrorDetail(&anypb.Any{TypeUrl: "type.googleapis.com/" + detail.Type(), Value: raw})
		if e != nil {
			return currentEncodingLimit()
		}
		result.AddDetail(frozen)
	}
	return result
}

func isCurrentPublicFailure(ctx context.Context) bool {
	r, _ := ctx.Value(currentOutputRequestKey{}).(*currentOutputRequest)
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.grant.kind == "public-failure"
}

// BindCurrentFailure is for the fixed, content-free HTTP/RPC boundary error
// schemas only. It clears staged protected metadata (notably cookie issuance).
// Ordinary legacy writers are unchanged. Once a unit started, no replacement
// permit is possible; the stream must finish under its original grant or abort.
func BindCurrentFailure(w http.ResponseWriter) {
	for depth := 0; depth < 16; depth++ {
		if owned, ok := w.(*currentOutputWriter); ok {
			bindCurrentFailure(owned)
			return
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = unwrapper.Unwrap()
	}
}
func bindCurrentFailure(w *currentOutputWriter) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.request
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	clear(w.headers)
	r.grant = currentOutputGrant{kind: "public-failure", resource: s2cHash("public-fixed-failure", CurrentGenericFailure)}
}
