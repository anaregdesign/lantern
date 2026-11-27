package dataruntime

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/tarp"
)

// RestoreGraphStorage reconstructs derived graph state and returns the writer
// used to stage mutations into that same database.
func RestoreGraphStorage(ctx context.Context, graph *graphcache.GraphCache[string, *pb.Vertex], db *graphstore.DB) (TarpGraphStager, error) {
	rows, err := graphstorage.Rows()
	if err != nil {
		return nil, err
	}
	view, err := db.Read()
	if err != nil {
		return nil, err
	}
	err = graph.RestoreStorage(ctx, rows, view)
	if err = errors.Join(err, view.Close()); err != nil {
		return nil, err
	}
	stage := func(ctx context.Context, gen uint64, snap *tarp.Snapshot, batch graphstore.Writer, keys graphcache.StorageKeys[string], writer graphcache.StorageRowWriter[string, *pb.Vertex]) error {
		if writer == nil {
			writer = graph
		}
		return writer.StoreRows(ctx, rows, snap, batch, gen, keys)
	}
	return stage, nil
}

// OpenReceiptDatabase opens a receipt-enabled runtime for an embedded Server.
// Fresh mode initializes the application records, while restart requires the
// existing node, namespace and immutable receipt policy to match.
func OpenReceiptDatabase(ctx context.Context, cfg ReceiptDatabaseConfig, fresh bool) (*ServingRuntime, error) {
	if err := validateReceiptDatabaseConfig(cfg); err != nil {
		return nil, err
	}
	open := graphstore.OpenExisting
	if fresh {
		open = graphstore.Open
	}
	db, err := open(ctx, cfg.Path, cfg.NodeID)
	if err != nil {
		return nil, err
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](cfg.DefaultTTL)
	if err := durableRuntimeGraphPolicy(cfg)(graph); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	stage, err := RestoreGraphStorage(ctx, graph, db)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	rt, err := NewTarpReceiptServingRuntime(ctx, graph, db, stage, cfg, fresh)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	db.StartMaintenance()
	return rt, nil
}

// OpenGraphDatabase binds an embedded graph-only Server to its durable graph
// and origin history. The caller supplies an empty graph with its query policy.
func OpenGraphDatabase(ctx context.Context, dir string, node hlc.NodeID, graph *graphcache.GraphCache[string, *pb.Vertex], log mutationlog.Options, namespace string) (*ServingRuntime, error) {
	if err := ensureRuntimeEdgeHistory(graph, namespace); err != nil {
		return nil, err
	}
	db, err := graphstore.Open(ctx, dir, node)
	if err != nil {
		return nil, err
	}
	stage, err := RestoreGraphStorage(ctx, graph, db)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	rt, err := NewTarpGraphServingRuntime(ctx, graph, db, log, stage, namespace)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	db.StartMaintenance()
	return rt, nil
}
