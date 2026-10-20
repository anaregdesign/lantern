package replication

import (
	"encoding/hex"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Kept identical to service.SnapshotTailHeader without coupling the outbound
// replication driver to the service implementation.
const snapshotTailHeader = "Lantern-Snapshot-Tail"

// snapshotTailStream checks the transport handoff before exposing the first
// Snapshot frame to an installer. A token never makes an incomplete image a
// verified Snapshot; the complete installer still owns that decision.
type snapshotTailStream struct {
	stream interface {
		SnapshotStream
		ResponseHeader() http.Header
	}
	require bool
	checked bool
	token   string
	failure error
}

func (s *snapshotTailStream) Receive() bool {
	if s.failure != nil || !s.stream.Receive() {
		return false
	}
	if !s.checked {
		s.checked = true
		values := s.stream.ResponseHeader().Values(snapshotTailHeader)
		if s.require {
			if len(values) != 1 || len(values[0]) != 64 {
				s.failure = connect.NewError(connect.CodeFailedPrecondition, errors.New("Snapshot tail reservation metadata is missing or malformed"))
				return false
			}
			if _, err := hex.DecodeString(values[0]); err != nil {
				s.failure = connect.NewError(connect.CodeFailedPrecondition, errors.New("Snapshot tail reservation token is malformed"))
				return false
			}
			s.token = values[0]
		} else if len(values) != 0 {
			s.failure = connect.NewError(connect.CodeFailedPrecondition, errors.New("unsolicited Snapshot tail reservation"))
			return false
		}
	}
	return true
}

func (s *snapshotTailStream) Msg() *pb.SnapshotResponse { return s.stream.Msg() }
func (s *snapshotTailStream) Err() error {
	if s.failure != nil {
		return s.failure
	}
	return s.stream.Err()
}
