package replication

import (
	"errors"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func compatibleDataNamespace(local, remote string) error {
	if err := keyspace.ValidateFormat(local); err != nil || local != remote {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("peer namespace format is incompatible"))
	}
	return nil
}

func validateDataMutation(mutation *pb.Mutation, format string) error {
	if mutation == nil {
		return errors.New("nil peer mutation")
	}
	if err := compatibleDataNamespace(format, mutation.GetNamespaceFormat()); err != nil {
		return err
	}
	if format != "" {
		return keyspace.ValidatePhysicalGraphMessage(mutation.ProtoReflect())
	}
	return nil
}

// namespaceSnapshotStream validates each physical frame before an installer
// can observe it. The header pins the format before any graph publication.
type namespaceSnapshotStream struct {
	stream  SnapshotStream
	format  string
	failure error
}

func (s *namespaceSnapshotStream) Receive() bool {
	if s.failure != nil || !s.stream.Receive() {
		return false
	}
	frame := s.stream.Msg()
	if frame == nil {
		s.failure = errors.New("nil snapshot frame")
		return false
	}
	if header := frame.GetHeader(); header != nil {
		s.failure = compatibleDataNamespace(s.format, header.GetNamespaceFormat())
	}
	if s.failure == nil && s.format != "" {
		s.failure = keyspace.ValidatePhysicalGraphMessage(frame.ProtoReflect())
	}
	return s.failure == nil
}
func (s *namespaceSnapshotStream) Msg() *pb.SnapshotResponse { return s.stream.Msg() }
func (s *namespaceSnapshotStream) Err() error {
	if s.failure != nil {
		return s.failure
	}
	return s.stream.Err()
}
