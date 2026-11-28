package provider

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func newTarpServingRuntime(ctx context.Context, receipt ReceiptConfig, cacheConfig CacheConfig, search SearchConfig, replication ReplicationConfig, logOptions mutationlog.Options, backups backup.Config) (*dataruntime.ServingRuntime, error) {
	if cacheConfig.DataDir == "" {
		return nil, errors.New("LANTERN_DATA_DIR must identify a node-owned directory")
	}
	dir, err := filepath.Abs(cacheConfig.DataDir)
	if err != nil {
		return nil, err
	}
	var expected hlc.NodeID
	if replication.nodeIDExplicit {
		expected = replication.NodeID
	}
	open := graphstore.Open
	switch receipt.Mode {
	case ReceiptModeFresh:
		if backups.RestoreRequired {
			return nil, errors.New("receipt fresh mode cannot satisfy required restore")
		}
	case ReceiptModeRestart:
		// A self-consistent old backup cannot prove that the same receipt
		// epoch acknowledged no later writes. Operators restore a witnessed
		// final cut explicitly before restarting a missing node.
		open = graphstore.OpenExisting
	case ReceiptModeGraphOnly:
		if err := backup.RestoreGraphStoreOnStartup(ctx, dir, expected, backups); err != nil {
			return nil, err
		}
		if backups.RestoreRequired {
			open = graphstore.OpenExisting
		}
	}
	db, err := open(ctx, dir, expected)
	if err != nil {
		return nil, err
	}
	cacheSearch := search
	cacheSearch.Enabled = false
	rt, err := restoreTarpRuntime(ctx, db, receipt, cacheConfig, cacheSearch, logOptions)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if search.Enabled {
		project := vertexSearchDocument
		if cacheConfig.namespaceFormat != "" {
			project = namespacedVertexSearchDocument
		}
		if err := rt.StartDiskSearch(ctx, dir+".search", search.Positions, search.AnalysisLimits, project); err != nil {
			return nil, errors.Join(fmt.Errorf("open Tarp Search: %w", err), rt.Close())
		}
	} else if err := rt.ClearDiskSearchSelection(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("clear Tarp Search selection: %w", err), rt.Close())
	}
	db.StartMaintenance()
	return rt, nil
}

func restoreTarpRuntime(ctx context.Context, db *graphstore.DB, receipt ReceiptConfig, cacheConfig CacheConfig, search SearchConfig, logOptions mutationlog.Options) (*dataruntime.ServingRuntime, error) {
	graph := NewGraphCache(cacheConfig, search)
	stage, err := dataruntime.RestoreGraphStorage(ctx, graph, db)
	if err != nil {
		return nil, err
	}
	if receipt.Mode == ReceiptModeGraphOnly {
		return dataruntime.NewTarpGraphServingRuntime(ctx, graph, db, logOptions, stage, cacheConfig.namespaceFormat)
	}
	now := time.Now()
	config := dataruntime.ReceiptDatabaseConfig{
		NamespaceFormat: cacheConfig.namespaceFormat,
		Receipt:         receipt.receiptConfig(now),
		Log:             logOptions,
		DefaultTTL:      cacheConfig.TTL,
		ConfigureGraph:  receiptGraphConfigurator(cacheConfig, search),
		NodeID:          db.NodeID(),
		Now:             now,
		BaselineCodec:   backup.ReceiptBaselineCodec{},
	}
	return dataruntime.NewTarpReceiptServingRuntime(ctx, graph, db, stage, config, receipt.Mode == ReceiptModeFresh)
}
