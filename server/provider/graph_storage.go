package provider

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func newTarpServingRuntime(ctx context.Context, receipt ReceiptWALConfig, cacheConfig CacheConfig, search SearchConfig, replication ReplicationConfig, logOptions mutationlog.Options, backups backup.Config) (*dataruntime.ServingRuntime, error) {
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
	case ReceiptWALModeFresh:
		if backups.RestoreRequired {
			return nil, errors.New("receipt fresh mode cannot satisfy required restore")
		}
	case ReceiptWALModeRestart:
		// A self-consistent old backup cannot prove that the same receipt
		// epoch acknowledged no later writes. Operators restore a witnessed
		// final cut explicitly before restarting a missing node.
		open = graphstore.OpenExisting
	case ReceiptWALModeGraphOnly:
		if err := backup.RestoreGraphStoreOnStartup(ctx, dir, expected, backups); err != nil {
			return nil, err
		}
	}
	db, err := open(ctx, dir, expected)
	if err != nil {
		return nil, err
	}
	rt, err := restoreTarpRuntime(ctx, db, receipt, cacheConfig, search, logOptions)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	db.StartReclamation()
	return rt, nil
}

func restoreTarpRuntime(ctx context.Context, db *graphstore.DB, receipt ReceiptWALConfig, cacheConfig CacheConfig, search SearchConfig, logOptions mutationlog.Options) (*dataruntime.ServingRuntime, error) {
	graph := NewGraphCache(cacheConfig, search)
	stage, err := dataruntime.RestoreGraphStorage(ctx, graph, db)
	if err != nil {
		return nil, err
	}
	if receipt.Mode == ReceiptWALModeGraphOnly {
		return dataruntime.NewTarpGraphServingRuntime(graph, db, logOptions, stage, cacheConfig.namespaceFormat)
	}
	now := time.Now()
	config := dataruntime.DurableReceiptWALRuntimeConfig{
		NamespaceFormat: cacheConfig.namespaceFormat,
		Receipt:         receipt.receiptConfig(now),
		Log:             logOptions,
		DefaultTTL:      cacheConfig.TTL,
		ConfigureGraph:  receiptWALGraphConfigurator(cacheConfig, search),
		NodeID:          db.NodeID(),
		Now:             now,
		BaselineCodec:   backup.ReceiptBaselineCodec{},
	}
	return dataruntime.NewTarpReceiptServingRuntime(graph, db, stage, config, receipt.Mode == ReceiptWALModeFresh)
}
