package client

import (
	"context"
	"fmt"
	"math"
	"unicode/utf8"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// EdgeContributionRef identifies one Add row by its directed endpoints and
// nonzero, caller-known 24-byte ContribID. Put bases are not Add rows.
type EdgeContributionRef struct {
	Tail      string
	Head      string
	ContribID ContribID
}

// DeleteEdgeContributions removes just the selected Add rows, preserving
// other contributions and any Put base. Existed is index-aligned even for
// duplicate references or missing/expired IDs; deleted counts true results.
//
// Batches are chunked. On failure the returned existed slice and deleted
// count describe only the fully observed prefix; *BatchError.Written gives
// its input length. A failed chunk may have committed: an unreceipted retry
// can return false after a previous successful deletion.
func (l *Lantern) DeleteEdgeContributions(ctx context.Context, refs []EdgeContributionRef) (existed []bool, deleted int, err error) {
	keys, err := edgeContributionKeys(refs)
	if err != nil {
		return nil, 0, err
	}
	if len(keys) == 0 {
		return []bool{}, 0, nil
	}
	existed = make([]bool, 0, len(keys))
	undisclosed := false
	deleted, err = runBatchWrite(ctx, l, keys, func(ctx context.Context, chunk []*pb.EdgeContributionKey) (int32, error) {
		response, callErr := unary(ctx, l, &pb.DeleteEdgeContributionsRequest{Contributions: chunk}, l.client.DeleteEdgeContributions)
		if callErr != nil {
			return 0, callErr
		}
		if err := mutationAcceptanceFromProto(response); err != nil {
			if _, accepted := err.(*MutationAcceptance); accepted {
				undisclosed = true
			}
			return 0, err
		}
		if checkErr := validateContributionDeleteResponse(len(chunk), response); checkErr != nil {
			return 0, checkErr
		}
		existed = append(existed, response.GetExisted()...)
		return response.GetDeleted(), nil
	})
	if undisclosed {
		return nil, 0, err
	}
	return existed, deleted, err
}

// DeleteEdgeContribution is the one-item facade over DeleteEdgeContributions;
// unlike DeleteEdge it never removes the entire (tail, head) edge.
func (l *Lantern) DeleteEdgeContribution(ctx context.Context, tail, head string, id ContribID) (bool, error) {
	existed, _, err := l.DeleteEdgeContributions(ctx, []EdgeContributionRef{{Tail: tail, Head: head, ContribID: id}})
	if err != nil {
		return false, err
	}
	if len(existed) != 1 {
		return false, fmt.Errorf("lantern: server returned %d outcomes for one contribution", len(existed))
	}
	return existed[0], nil
}

func edgeContributionKeys(refs []EdgeContributionRef) ([]*pb.EdgeContributionKey, error) {
	if uint64(len(refs)) > math.MaxInt32 {
		return nil, fmt.Errorf("%w: contribution Delete exceeds %d items", ErrInvalidArgument, int64(math.MaxInt32))
	}
	keys := make([]*pb.EdgeContributionKey, len(refs))
	for i, ref := range refs {
		if ref.Tail == "" || ref.Head == "" || !utf8.ValidString(ref.Tail) || !utf8.ValidString(ref.Head) {
			return nil, fmt.Errorf("%w: contributions[%d] requires nonempty UTF-8 tail and head", ErrInvalidArgument, i)
		}
		if ref.ContribID == (ContribID{}) {
			return nil, fmt.Errorf("%w: contributions[%d] ID must be a nonzero 24-byte ContribID", ErrInvalidArgument, i)
		}
		keys[i] = &pb.EdgeContributionKey{Tail: ref.Tail, Head: ref.Head, ContribId: ref.ContribID.Bytes()}
	}
	return keys, nil
}

func validateContributionDeleteResponse(count int, response *pb.DeleteEdgeContributionsResponse) error {
	if response == nil || len(response.GetExisted()) != count {
		return fmt.Errorf("lantern: server returned %d contribution Delete outcomes for %d inputs", len(response.GetExisted()), count)
	}
	var deleted int32
	for _, existed := range response.GetExisted() {
		if existed {
			deleted++
		}
	}
	if response.GetDeleted() != deleted {
		return fmt.Errorf("lantern: server returned contribution Delete count %d, want %d", response.GetDeleted(), deleted)
	}
	return nil
}
