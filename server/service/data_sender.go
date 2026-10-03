package service

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// dataBackupSender decodes only owned public data frames. Private peer
// snapshots never use it and keep their physical identities.
type dataBackupSender struct {
	ctx     context.Context
	service *LanternService
	next    Sender[pb.BackupSnapshotResponse]
}

func (s dataBackupSender) Send(frame *pb.BackupSnapshotResponse) error {
	logical, err := s.service.mapDataResponse(frame, nil)
	if err != nil {
		return err
	}
	if s.service.dataAuthorization {
		if _, err := s.service.dataAdmission(s.ctx); err != nil {
			return connect.NewError(connect.CodeUnavailable, err)
		}
	}
	return s.next.Send(logical.(*pb.BackupSnapshotResponse))
}
