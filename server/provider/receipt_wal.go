package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
)

// ReceiptWALMode selects the process state owner before any listener or
// replication worker is constructed.
type ReceiptWALMode string

const (
	ReceiptWALModeGraphOnly ReceiptWALMode = "graph-only"
	ReceiptWALModeFresh     ReceiptWALMode = "fresh"
	ReceiptWALModeRestart   ReceiptWALMode = "restart"
)

// ReceiptWALConfig is the durable-runtime contract.
//
//   - LANTERN_RECEIPT_MODE            graph-only (default), fresh, or restart
//   - LANTERN_RECEIPT_EPOCH           nonzero 32-hex-character deployment epoch
//   - LANTERN_RECEIPT_RETENTION       immutable one-hour to 30-day Go duration
//   - LANTERN_RECEIPT_MAX_ENTRIES     positive retained-receipt entry cap
//   - LANTERN_RECEIPT_MAX_BYTES       positive retained-receipt logical byte cap
//   - LANTERN_NODE_ID                 explicit nonzero stable origin identity
//
// Durable construction alone does not enable a public receipt surface. Final
// activation additionally requires runtime, backup, recovery, and bearer-auth
// certification. Fresh never overwrites existing bytes; restart requires the
// matching Tarp database and receipt policy.
type ReceiptWALConfig struct {
	Mode       ReceiptWALMode
	Epoch      mutationreceipt.Epoch
	Retention  time.Duration
	MaxEntries int
	MaxBytes   int
}

func loadReceiptWALConfig(reader *envconfig.Reader) ReceiptWALConfig {
	mode := ReceiptWALMode(strings.TrimSpace(reader.String("LANTERN_RECEIPT_MODE", string(ReceiptWALModeGraphOnly))))
	epochText := strings.TrimSpace(reader.String("LANTERN_RECEIPT_EPOCH", ""))
	retention := reader.Duration("LANTERN_RECEIPT_RETENTION", 0)
	maxEntries := reader.Int("LANTERN_RECEIPT_MAX_ENTRIES", 0)
	maxBytes := reader.Int("LANTERN_RECEIPT_MAX_BYTES", 0)

	var epoch mutationreceipt.Epoch
	if epochText != "" {
		decoded, err := hex.DecodeString(epochText)
		if err != nil || len(decoded) != len(epoch) {
			reader.Malformed("LANTERN_RECEIPT_EPOCH", epochText, "must be exactly 32 hexadecimal characters")
		} else {
			copy(epoch[:], decoded)
		}
	}
	return ReceiptWALConfig{
		Mode: mode, Epoch: epoch, Retention: retention,
		MaxEntries: maxEntries, MaxBytes: maxBytes,
	}
}

func validateReceiptWALConfig(
	config ReceiptWALConfig,
	backups backup.Config,
	replication ReplicationConfig,
) error {
	if replication.nodeIDExplicit && replication.NodeID == (hlc.NodeID{}) {
		return errors.New("LANTERN_NODE_ID must be a nonzero 32-hex-character value")
	}
	switch config.Mode {
	case ReceiptWALModeGraphOnly:
		if config.Epoch != (mutationreceipt.Epoch{}) ||
			config.Retention != 0 || config.MaxEntries != 0 || config.MaxBytes != 0 {
			return errors.New("LANTERN_RECEIPT_MODE=graph-only cannot be combined with durable receipt WAL settings")
		}
		return nil
	case ReceiptWALModeFresh, ReceiptWALModeRestart:
	default:
		return fmt.Errorf("LANTERN_RECEIPT_MODE must be exactly graph-only, fresh, or restart")
	}
	if config.Epoch == (mutationreceipt.Epoch{}) {
		return errors.New("LANTERN_RECEIPT_EPOCH must be a nonzero 32-hex-character value in receipt mode")
	}
	if !replication.nodeIDExplicit || replication.NodeID == (hlc.NodeID{}) {
		return errors.New("LANTERN_NODE_ID must be explicitly set to a nonzero 32-hex-character value in receipt mode")
	}
	if config.MaxBytes <= 0 {
		return errors.New("LANTERN_RECEIPT_MAX_BYTES must be positive in receipt mode")
	}
	if _, err := mutationreceipt.New(config.receiptConfig(time.Time{})); err != nil {
		return fmt.Errorf("durable receipt WAL policy: %w", err)
	}
	if backups.RestoreRequired && !backups.RestoreOnStart {
		return errors.New("LANTERN_BACKUP_RESTORE_REQUIRED requires LANTERN_BACKUP_RESTORE_ON_START in receipt mode")
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
// opens the node-owned graph database and binds the selected receipt policy
// before any selector exposes state.
func NewServingRuntime(
	config ReceiptWALConfig,
	cacheConfig CacheConfig,
	searchConfig SearchConfig,
	logConfig MutationLogConfig,
	replicationConfig ReplicationConfig,
	backups backup.Config,
	metrics *domainmetrics.DomainMetrics,
) (_ *dataruntime.ServingRuntime, cleanup func(), err error) {
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

	runtime, err := newTarpServingRuntime(context.Background(), config, cacheConfig, searchConfig, replicationConfig, options, backups)
	if err != nil {
		return nil, nil, err
	}
	return runtime, func() { _ = runtime.Close() }, nil
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

// runtimeCertified is an identity-bearing ordering marker that cannot be
// constructed outside this package. Listener and replication providers
// require it, so Wire cannot bind network-facing objects to foreign state.
type runtimeCertified struct {
	valid                   bool
	runtime                 *dataruntime.ServingRuntime
	primary                 *dataruntime.Executor
	replication             *dataruntime.Replication
	replicationSendMaxBytes int
}

// runtimeRestored is the identity-bearing startup-restore barrier. Even a
// no-op graph-only or healthy-restart path passes through it, so Wire cannot
// certify a runtime before any pending durable baseline has committed.
type runtimeRestored struct {
	valid   bool
	runtime *dataruntime.ServingRuntime
	primary *dataruntime.Executor
}

func NewRuntimeRestored(
	runtime *dataruntime.ServingRuntime,
	primary *dataruntime.Executor,
) (runtimeRestored, error) {
	if runtime == nil || primary == nil {
		return runtimeRestored{}, errors.New("startup restore requires the serving runtime and primary service")
	}
	if err := runtime.CompleteStartupRestore(context.Background(), primary); err != nil {
		return runtimeRestored{}, err
	}
	return runtimeRestored{valid: true, runtime: runtime, primary: primary}, nil
}

// publicReceiptsCertified is the final pre-listener activation barrier. Wire
// can construct it only after the exact runtime is certified and its
// production backup path has been bound.
type publicReceiptsCertified struct {
	valid bool
}

// NewRuntimeCertified completes the production composition barrier.
func NewRuntimeCertified(
	runtime *dataruntime.ServingRuntime,
	primary *dataruntime.Executor,
	replication *dataruntime.Replication,
	restored runtimeRestored,
	netConfig NetConfig,
) (runtimeCertified, error) {
	if runtime == nil || primary == nil || replication == nil ||
		!restored.valid || restored.runtime != runtime || restored.primary != primary {
		return runtimeCertified{}, errors.New("receipt WAL runtime certification requires both service surfaces")
	}
	if err := runtime.CertifyInstallationWithReplicationSendLimit(
		primary,
		replication,
		netConfig.MaxSendMsgBytes,
	); err != nil {
		return runtimeCertified{}, fmt.Errorf("certify serving runtime: %w", err)
	}
	return runtimeCertified{
		valid:                   true,
		runtime:                 runtime,
		primary:                 primary,
		replication:             replication,
		replicationSendMaxBytes: netConfig.MaxSendMsgBytes,
	}, nil
}

// NewOIDCPublicReceiptsCertified activates receipts only after the exact data
// service has its native Principal/Role boundary installed.
func NewOIDCPublicReceiptsCertified(config ReceiptWALConfig, security *SecurityRuntime, runtime *dataruntime.ServingRuntime, primary *dataruntime.Executor, backupper *backup.Backupper, certified runtimeCertified, public publicSecurityCertified) (publicReceiptsCertified, error) {
	if security == nil || public.runtime != security || security.data != runtime {
		return publicReceiptsCertified{}, errors.New("receipt activation requires the exact public security runtime")
	}
	if public.backup != backupper || backupper == nil || !backupper.UsesExecutor(primary) {
		return publicReceiptsCertified{}, errors.New("receipt activation requires the exact managed backup producer")
	}
	return certifyPublicReceipts(config, security.mode == "oidc", runtime, primary, certified)
}

func certifyPublicReceipts(config ReceiptWALConfig, authenticated bool, runtime *dataruntime.ServingRuntime, primary *dataruntime.Executor, certified runtimeCertified) (publicReceiptsCertified, error) {
	if !certified.valid || runtime == nil || primary == nil ||
		certified.runtime != runtime || certified.primary != primary ||
		certified.replication == nil {
		return publicReceiptsCertified{}, errors.New("public receipt activation requires the exact certified serving runtime")
	}
	switch config.Mode {
	case ReceiptWALModeGraphOnly:
		if runtime.DurableReceiptWAL() {
			return publicReceiptsCertified{}, errors.New("public receipt activation mode differs from the serving runtime")
		}
	case ReceiptWALModeFresh, ReceiptWALModeRestart:
		if !runtime.DurableReceiptWAL() {
			return publicReceiptsCertified{}, errors.New("public receipt activation mode differs from the serving runtime")
		}
		if authenticated {
			if err := runtime.ActivatePublicReceipts(primary, certified.replication); err != nil {
				return publicReceiptsCertified{}, fmt.Errorf("activate public receipts: %w", err)
			}
		}
	default:
		return publicReceiptsCertified{}, errors.New("public receipt activation received an invalid runtime mode")
	}
	return publicReceiptsCertified{valid: true}, nil
}

func NewRuntimeGraph(runtime *dataruntime.ServingRuntime) *graphcache.GraphCache[string, *pb.Vertex] {
	return runtime.GraphCache()
}
