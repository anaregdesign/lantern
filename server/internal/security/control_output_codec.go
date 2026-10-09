package security

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// The private initial transport profile uses protobuf Connect over HTTP/1.1.
// Pre-size before allocation: Connect's ordinary send limit checks only after
// encoding. The wrapper reserves encoder credit before entering this codec.
type authorityOutputCodec struct{}

func (authorityOutputCodec) Name() string { return "proto" }
func (authorityOutputCodec) Marshal(v any) ([]byte, error) {
	return (authorityOutputCodec{}).MarshalAppend(nil, v)
}
func (authorityOutputCodec) MarshalAppend(dst []byte, v any) ([]byte, error) {
	m, ok := v.(proto.Message)
	if !ok || proto.Size(m)+len(dst) > authorityOutputUnitBytes-5 {
		return nil, errS3ACredit
	}
	return proto.MarshalOptions{}.MarshalAppend(dst, m)
}
func (authorityOutputCodec) Unmarshal(raw []byte, v any) error {
	m, ok := v.(proto.Message)
	if !ok || len(raw) > authorityOutputUnitBytes {
		return errS3ACredit
	}
	return proto.Unmarshal(raw, m)
}
func authorityOutputOptions() []connect.HandlerOption {
	return []connect.HandlerOption{connect.WithCodec(authorityOutputCodec{}), connect.WithSendMaxBytes(authorityOutputUnitBytes - 5), connect.WithReadMaxBytes(authorityOutputUnitBytes), connect.WithCompressMinBytes(int(^uint(0) >> 1)), connect.WithInterceptors(authorityOutputInterceptor{})}
}

func authorityOutputMessageSize(v any) error {
	m, ok := v.(proto.Message)
	if !ok || proto.Size(m) > authorityOutputUnitBytes-5 {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("response exceeds finite current-output unit"))
	}
	return nil
}

type authorityOutputInterceptor struct{}

// Connect encodes errors and end-stream metadata outside the protobuf codec.
// Preserve only the status code: service text, details and metadata have no
// finite encoding bound and must not enter that path. This private transport
// deliberately exposes one bounded message for every service error.
func authorityOutputError(err error) error {
	if err == nil {
		return nil
	}
	code := connect.CodeOf(err)
	if errors.Is(err, context.Canceled) {
		code = connect.CodeCanceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = connect.CodeDeadlineExceeded
	}
	return connect.NewError(code, errors.New("private current-authority operation unavailable"))
}

func authorityOutputMetadata(header, trailer http.Header) error {
	if _, err := authorityFreezeHeaders(header); err != nil {
		return connect.NewError(connect.CodeResourceExhausted, errS3ACredit)
	}
	if _, err := authorityFreezeHeaders(trailer); err != nil {
		return connect.NewError(connect.CodeResourceExhausted, errS3ACredit)
	}
	return nil
}

func (authorityOutputInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
		result, err := next(ctx, r)
		if err != nil {
			return nil, authorityOutputError(err)
		}
		if result == nil {
			return nil, authorityOutputError(connect.NewError(connect.CodeInternal, errS3ACredit))
		}
		if err := authorityOutputMetadata(result.Header(), result.Trailer()); err != nil {
			return nil, authorityOutputError(err)
		}
		if err := authorityOutputMessageSize(result.Any()); err != nil {
			return nil, err
		}
		return result, nil
	}
}
func (authorityOutputInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
func (authorityOutputInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, c connect.StreamingHandlerConn) error {
		protocolHeaders, headerErr := authorityFreezeHeaders(c.ResponseHeader())
		if headerErr != nil {
			clear(c.ResponseHeader())
			clear(c.ResponseTrailer())
			return authorityOutputError(connect.NewError(connect.CodeResourceExhausted, headerErr))
		}
		err := next(ctx, authorityOutputStream{c})
		if metadataErr := authorityOutputMetadata(c.ResponseHeader(), c.ResponseTrailer()); metadataErr != nil {
			// Connect retains the same maps for its end-stream JSON serialization.
			// Remove the oversized inventory before handing control back to it.
			if _, err := authorityFreezeHeaders(c.ResponseHeader()); err != nil {
				clear(c.ResponseHeader())
				for key, values := range protocolHeaders {
					c.ResponseHeader()[key] = values
				}
			}
			clear(c.ResponseTrailer())
			err = metadataErr
		}
		return authorityOutputError(err)
	}
}

type authorityOutputStream struct{ connect.StreamingHandlerConn }

func (s authorityOutputStream) Send(v any) error {
	if err := authorityOutputMetadata(s.ResponseHeader(), s.ResponseTrailer()); err != nil {
		return authorityOutputError(err)
	}
	if v != nil {
		if err := authorityOutputMessageSize(v); err != nil {
			return err
		}
	}
	return s.StreamingHandlerConn.Send(v)
}

// A request owns its fresh Connect handler/encoding pool. Keeping a shared
// Connect sync.Pool would not supply a finite retained-byte invariant across
// arbitrary request histories. Only the stable typed service function is kept.
type authorityOutputRoute struct {
	path   string
	create func() http.Handler
}

func authorityOutputUnary[Req, Res any](path string, fn func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error)) authorityOutputRoute {
	return authorityOutputRoute{path, func() http.Handler { return connect.NewUnaryHandler(path, fn, authorityOutputOptions()...) }}
}
func authorityOutputStreaming[Req, Res any](path string, fn func(context.Context, *connect.Request[Req], *connect.ServerStream[Res]) error) authorityOutputRoute {
	return authorityOutputRoute{path, func() http.Handler { return connect.NewServerStreamHandler(path, fn, authorityOutputOptions()...) }}
}
