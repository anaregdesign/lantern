// Package client: subscribe.go owns the SDK's replication Subscribe
// helper. Unlike the unary RPCs that ride graphv1connect's primary
// LanternServiceClient, Subscribe is a server-stream off the
// LanternReplicationService surface, so it builds its own per-call
// Connect client against the same baseURL the *Lantern was
// constructed with.
package client

import (
	"context"
	"errors"
	"io"
	"iter"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// Subscribe opens a server-stream against the replication service and
// returns an iter.Seq2 yielding successive *SubscribeResponse frames
// until the stream closes. fromSeq passes through unchanged.
//
// Stop conditions: clean EOF, any server error, the supplied ctx being
// canceled, or the consumer returning false from yield. The underlying
// *connect.ServerStreamForClient is always closed before iteration
// ends.
func (l *Lantern) Subscribe(ctx context.Context, fromSeq uint64) iter.Seq2[*pb.SubscribeResponse, error] {
	return func(yield func(*pb.SubscribeResponse, error) bool) {
		rc := graphv1connect.NewLanternReplicationServiceClient(l.httpClient, l.baseURL)
		stream, err := rc.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromSeq: fromSeq}))
		if err != nil {
			yield(nil, wrapConnectErr(err))
			return
		}
		defer func() { _ = stream.Close() }()
		for stream.Receive() {
			if !yield(stream.Msg(), nil) {
				return
			}
		}
		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			yield(nil, wrapConnectErr(err))
		}
	}
}
