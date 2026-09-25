package provider

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/service"
)

// ReceiptWALMode selects the process state owner before any listener or
// replication worker is constructed.
type ReceiptWALMode string

const (
	ReceiptWALModeGraphOnly ReceiptWALMode = "graph-only"
	ReceiptWALModeFresh     ReceiptWALMode = "fresh"
	ReceiptWALModeRestart   ReceiptWALMode = "restart"
)

// ReceiptWALConfig is the private durable-runtime contract.
//
//   - LANTERN_RECEIPT_WAL_MODE        graph-only (default), fresh, or restart
//   - LANTERN_RECEIPT_WAL_PATH        absolute FileWAL path; required in durable modes
//   - LANTERN_RECEIPT_EPOCH           nonzero 32-hex-character deployment epoch
//   - LANTERN_RECEIPT_RETENTION       immutable one-hour to 30-day Go duration
//   - LANTERN_RECEIPT_MAX_ENTRIES     positive retained-receipt entry cap
//   - LANTERN_RECEIPT_MAX_BYTES       positive retained-receipt logical byte cap
//   - LANTERN_NODE_ID                 explicit nonzero stable origin identity
//
// Durable mode is private infrastructure. It does not enable receipt
// capability/status or any offline mutation surface. Fresh never overwrites
// existing bytes; restart requires the WAL and every bound sidecar.
type ReceiptWALConfig struct {
	Mode       ReceiptWALMode
	Path       string
	Epoch      mutationreceipt.Epoch
	Retention  time.Duration
	MaxEntries int
	MaxBytes   int
}

func loadReceiptWALConfig() ReceiptWALConfig {
	mode := ReceiptWALMode(strings.TrimSpace(envconfig.String("LANTERN_RECEIPT_WAL_MODE", string(ReceiptWALModeGraphOnly))))
	path := strings.TrimSpace(envconfig.String("LANTERN_RECEIPT_WAL_PATH", ""))
	epochText := strings.TrimSpace(envconfig.String("LANTERN_RECEIPT_EPOCH", ""))
	retention := envconfig.Duration("LANTERN_RECEIPT_RETENTION", 0)
	maxEntries := envconfig.Int("LANTERN_RECEIPT_MAX_ENTRIES", 0)
	maxBytes := envconfig.Int("LANTERN_RECEIPT_MAX_BYTES", 0)

	var epoch mutationreceipt.Epoch
	if epochText != "" {
		decoded, err := hex.DecodeString(epochText)
		if err != nil || len(decoded) != len(epoch) {
			envconfig.Malformed("LANTERN_RECEIPT_EPOCH", epochText, "must be exactly 32 hexadecimal characters")
		} else {
			copy(epoch[:], decoded)
		}
	}
	return ReceiptWALConfig{
		Mode: mode, Path: path, Epoch: epoch, Retention: retention,
		MaxEntries: maxEntries, MaxBytes: maxBytes,
	}
}

func validateReceiptWALConfig(
	config ReceiptWALConfig,
	backups backup.Config,
	replication ReplicationConfig,
) error {
	switch config.Mode {
	case ReceiptWALModeGraphOnly:
		if config.Path != "" || config.Epoch != (mutationreceipt.Epoch{}) ||
			config.Retention != 0 || config.MaxEntries != 0 || config.MaxBytes != 0 {
			return errors.New("LANTERN_RECEIPT_WAL_MODE=graph-only cannot be combined with durable receipt WAL settings")
		}
		return nil
	case ReceiptWALModeFresh, ReceiptWALModeRestart:
	default:
		return fmt.Errorf("LANTERN_RECEIPT_WAL_MODE must be exactly graph-only, fresh, or restart")
	}
	if config.Path == "" || !filepath.IsAbs(config.Path) {
		return errors.New("LANTERN_RECEIPT_WAL_PATH must be an absolute path in durable receipt WAL mode")
	}
	if config.Epoch == (mutationreceipt.Epoch{}) {
		return errors.New("LANTERN_RECEIPT_EPOCH must be a nonzero 32-hex-character value in durable receipt WAL mode")
	}
	if !replication.nodeIDExplicit || replication.NodeID == (hlc.NodeID{}) {
		return errors.New("LANTERN_NODE_ID must be explicitly set to a nonzero 32-hex-character value in durable receipt WAL mode")
	}
	if config.MaxBytes <= 0 {
		return errors.New("LANTERN_RECEIPT_MAX_BYTES must be positive in durable receipt WAL mode")
	}
	if _, err := mutationreceipt.New(config.receiptConfig(time.Time{})); err != nil {
		return fmt.Errorf("durable receipt WAL policy: %w", err)
	}
	if backups.Enabled || backups.RestoreOnStart {
		return errors.New("LANTERN_BACKUP_ENABLED and graph-only restore must be disabled in durable receipt WAL mode")
	}
	return nil
}

func (c ReceiptWALConfig) receiptConfig(clockHighWater time.Time) mutationreceipt.Config {
	return mutationreceipt.Config{
		Epoch:          c.Epoch,
		Retention:      c.Retention,
		MaxEntries:     c.MaxEntries,
		MaxBytes:       uint64(c.MaxBytes),
		ClockHighWater: clockHighWater,
	}
}

// NewReceiptWALConfig returns the ReceiptWALConfig slice of Config.
func NewReceiptWALConfig(c *Config) ReceiptWALConfig { return c.ReceiptWAL }

// NewServingRuntime is the sole production state-composition boundary. It
// creates the historical in-memory graph-only runtime or certifies one
// lease-owned durable fresh/restart bundle before any selector exposes state.
func NewServingRuntime(
	config ReceiptWALConfig,
	cacheConfig CacheConfig,
	searchConfig SearchConfig,
	logConfig MutationLogConfig,
	replicationConfig ReplicationConfig,
	backups backup.Config,
	metrics *domainmetrics.DomainMetrics,
) (_ *service.ServingRuntime, cleanup func(), err error) {
	if err := validateReceiptWALConfig(config, backups, replicationConfig); err != nil {
		return nil, nil, err
	}
	options := mutationlog.Options{
		Capacity:         logConfig.Capacity,
		SubscriberBuffer: logConfig.SubscriberBuffer,
	}
	if metrics != nil {
		options.OnDrop = metrics.OnMutationLogSubscriberDropped
		metrics.SetMutationLogCapacity(logConfig.Capacity)
	}

	var runtime *service.ServingRuntime
	switch config.Mode {
	case ReceiptWALModeGraphOnly:
		graph := NewGraphCache(cacheConfig, searchConfig)
		log := mutationlog.New(options)
		runtime, err = service.NewGraphOnlyServingRuntime(graph, log, NewHLCClock(replicationConfig))
		if err != nil {
			_ = log.Close()
			return nil, nil, err
		}
	case ReceiptWALModeFresh, ReceiptWALModeRestart:
		now := time.Now()
		runtimeConfig := service.DurableReceiptWALRuntimeConfig{
			Path:           config.Path,
			Receipt:        config.receiptConfig(now),
			Log:            options,
			DefaultTTL:     cacheConfig.TTL,
			ConfigureGraph: receiptWALGraphConfigurator(cacheConfig, searchConfig),
			NodeID:         replicationConfig.NodeID,
			Now:            now,
			BaselineCodec:  backup.ReceiptBaselineCodec{},
		}
		if config.Mode == ReceiptWALModeFresh {
			runtime, err = service.CreateDurableReceiptWALServingRuntime(runtimeConfig)
		} else {
			runtime, err = service.OpenDurableReceiptWALServingRuntime(runtimeConfig)
		}
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("unsupported receipt WAL mode %q", config.Mode)
	}

	cleanup = func() { _ = runtime.Close() }
	return runtime, cleanup, nil
}

func receiptWALGraphConfigurator(
	cacheConfig CacheConfig,
	searchConfig SearchConfig,
) func(*graphcache.GraphCache[string, *pb.Vertex]) error {
	return func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		ConfigureGraphCache(graph, cacheConfig, searchConfig)
		return nil
	}
}

// runtimeCertified is an ordering marker that cannot be constructed outside
// this package. Listener and pump providers require it, so Wire cannot
// construct network-facing objects before both service surfaces exist.
type runtimeCertified struct{ valid bool }

// NewRuntimeCertified completes the production composition barrier.
func NewRuntimeCertified(
	runtime *service.ServingRuntime,
	primary *service.LanternService,
	replication *service.LanternReplicationService,
) (runtimeCertified, error) {
	if runtime == nil || primary == nil || replication == nil {
		return runtimeCertified{}, errors.New("receipt WAL runtime certification requires both service surfaces")
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		return runtimeCertified{}, fmt.Errorf("certify serving runtime: %w", err)
	}
	return runtimeCertified{valid: true}, nil
}

func NewRuntimeGraph(runtime *service.ServingRuntime) *graphcache.GraphCache[string, *pb.Vertex] {
	return runtime.GraphCache()
}
