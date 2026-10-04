package service

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type captureDataFrame struct{ frame *pb.BackupSnapshotResponse }

func (c *captureDataFrame) Send(frame *pb.BackupSnapshotResponse) error { c.frame = frame; return nil }

func TestDataBackupSender(t *testing.T) {
	next := &captureDataFrame{}
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace()
	sender := dataBackupSender{service: s, next: next}
	frame := &pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Vertex{Vertex: &pb.Vertex{Key: "data:sys:client"}}}
	if err := sender.Send(frame); err != nil || next.frame.GetVertex().GetKey() != "sys:client" || frame.GetVertex().GetKey() != "data:sys:client" {
		t.Fatalf("public backup frame mapping: %v", err)
	}
	next.frame = nil
	frame.GetVertex().Key = "sys:internal"
	if err := sender.Send(frame); err == nil || next.frame != nil {
		t.Fatal("system frame reached the public sender")
	}
}
