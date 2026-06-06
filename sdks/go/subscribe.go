// Package client: subscribe.go owns the SDK's replication Subscribe
// helper. Unlike the unary RPCs that ride graphv1connect's primary
// LanternServiceClient, Subscribe is a server-stream off the
// LanternReplicationService surface, so it builds its own per-call
// Connect client against the same baseURL the *Lantern was
// constructed with.
package client

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"iter"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// Subscribe opens a server-stream against the replication service and
// returns an iter.Seq2 yielding successive *SubscribeResponse frames
// until the stream closes.
//
// Under the leaderless Subscribe contract (#415), the cluster's full
// mutation stream is visible from every replica; cursor selects the
// resume point per origin. An empty cursor (nil or zero-length map)
// asks the server for every entry it still retains, suitable for a
// new consumer.
//
// Keys in the cursor are HLC NodeIDs of every origin the caller has
// already observed; values are the next seq the caller expects from
// that origin. Origins absent from the cursor are delivered from the
// oldest retained entry — see pb.SubscribeRequest for the full
// semantics.
//
// Stop conditions: clean EOF, any server error, the supplied ctx being
// canceled, or the consumer returning false from yield. The underlying
// *connect.ServerStreamForClient is always closed before iteration
// ends.
func (l *Lantern) Subscribe(ctx context.Context, cursor map[hlc.NodeID]uint64) iter.Seq2[*pb.SubscribeResponse, error] {
	return func(yield func(*pb.SubscribeResponse, error) bool) {
		wire := make(map[string]uint64, len(cursor))
		for id, seq := range cursor {
			wire[hex.EncodeToString(id[:])] = seq
		}
		rc := graphv1connect.NewLanternReplicationServiceClient(l.httpClient, l.baseURL)
		stream, err := rc.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromSeqPerOrigin: wire}))
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
