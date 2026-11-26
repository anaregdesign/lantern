package backup

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

// fakeService satisfies the backup Service: BackupSnapshot emits a fixed
// frame list; RestoreVertices / RestoreEdges record what restore replayed.
func TestBackupperUsesExactExecutorIdentity(t *testing.T) {
	primary := dataruntime.NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	owner := NewGraphStore(primary, nil, Config{Interval: time.Hour}, nil, nil)
	var absent *Backupper
	if !owner.UsesExecutor(primary) || owner.UsesExecutor(nil) || absent.UsesExecutor(primary) {
		t.Fatal("backup executor identity was not retained")
	}
	foreign := dataruntime.NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	if owner.UsesExecutor(foreign) || NewGraphStore(nil, nil, Config{}, nil, nil).UsesExecutor(primary) {
		t.Fatal("foreign backup service certified as the mounted executor")
	}
}
