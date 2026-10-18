package service

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestDataAuthorizationRequiresActualAdmission(t *testing.T) {
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace().WithDataAuthorization(time.Now)
	if _, err := s.authorizeData(t.Context(), &pb.GetVertexRequest{Key: "orders:1"}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("missing credential admitted", err)
	}
}
