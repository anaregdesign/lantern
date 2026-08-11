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
	"fmt"
	"io"
	"iter"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// ChangeOriginSize is the fixed byte width of every replication origin.
const ChangeOriginSize = 16

// ErrInvalidChangeOrigin reports a malformed replication origin.
var ErrInvalidChangeOrigin = errors.New("invalid change origin")

// ErrInvalidChangeEvent reports a Subscribe frame without a mutation.
var ErrInvalidChangeEvent = errors.New("invalid change event")

// ChangeOrigin identifies the node that originally accepted a mutation.
// It is deliberately SDK-local: the Go client depends on pb only and does
// not expose core/hlc types across its public boundary.
type ChangeOrigin [ChangeOriginSize]byte

// ChangeCursor stores the next sequence number expected from each origin.
// A nil or empty cursor asks Subscribe for every retained mutation.
type ChangeCursor map[ChangeOrigin]uint64

// ChangeEvent is the generated mutation value delivered by Subscribe. It is
// a true alias rather than a parallel SDK model.
type ChangeEvent = pb.Mutation

// ChangeOriginFromBytes validates and copies a wire mutation origin into the
// comparable SDK cursor key type.
func ChangeOriginFromBytes(origin []byte) (ChangeOrigin, error) {
	if len(origin) != ChangeOriginSize {
		return ChangeOrigin{}, fmt.Errorf("%w: must be %d bytes, got %d", ErrInvalidChangeOrigin, ChangeOriginSize, len(origin))
	}
	var result ChangeOrigin
	copy(result[:], origin)
	return result, nil
}

// ParseChangeOrigin decodes the 32-character lowercase hexadecimal form used
// by SubscribeRequest map keys and replication diagnostics.
func ParseChangeOrigin(origin string) (ChangeOrigin, error) {
	decoded, err := hex.DecodeString(origin)
	if err != nil {
		return ChangeOrigin{}, fmt.Errorf("%w: decode hex: %v", ErrInvalidChangeOrigin, err)
	}
	return ChangeOriginFromBytes(decoded)
}

// String returns the canonical lowercase hexadecimal representation.
func (o ChangeOrigin) String() string { return hex.EncodeToString(o[:]) }

// Subscribe opens a server-stream against the replication service and
// returns an iter.Seq2 yielding successive mutation events until the stream
// closes.
//
// Under the leaderless Subscribe contract (#415), the cluster's full
// mutation stream is visible from every replica; cursor selects the
// resume point per origin. An empty cursor (nil or zero-length map)
// asks the server for every entry it still retains, suitable for a
// new consumer.
//
// Keys in the cursor are opaque 16-byte origins the caller has already
// observed; values are the next seq the caller expects from that origin.
// Origins absent from the cursor are delivered from the oldest retained
// entry — see pb.SubscribeRequest for the full semantics.
//
// Stop conditions: clean EOF, any server error, the supplied ctx being
// canceled, or the consumer returning false from yield. The underlying
// *connect.ServerStreamForClient is always closed before iteration
// ends.
func (l *Lantern) Subscribe(ctx context.Context, cursor ChangeCursor) iter.Seq2[*ChangeEvent, error] {
	return func(yield func(*ChangeEvent, error) bool) {
		wire := make(map[string]uint64, len(cursor))
		for id, seq := range cursor {
			wire[id.String()] = seq
		}
		rc := graphv1connect.NewLanternReplicationServiceClient(l.httpClient, l.baseURL)
		stream, err := rc.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromSeqPerOrigin: wire}))
		if err != nil {
			yield(nil, wrapConnectErr(err))
			return
		}
		defer func() { _ = stream.Close() }()
		for stream.Receive() {
			mutation := stream.Msg().GetMutation()
			if mutation == nil {
				yield(nil, fmt.Errorf("%w: subscribe response missing mutation", ErrInvalidChangeEvent))
				return
			}
			if !yield(mutation, nil) {
				return
			}
		}
		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			yield(nil, wrapConnectErr(err))
		}
	}
}
