package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func validReceiptWALProviderConfig(path string, mode ReceiptWALMode) ReceiptWALConfig {
	return ReceiptWALConfig{
		Mode:       mode,
		Path:       path,
		Epoch:      mutationreceipt.Epoch{0x42},
		Retention:  time.Hour,
		MaxEntries: 32,
		MaxBytes:   1 << 20,
	}
}

func TestValidateReceiptWALConfig(t *testing.T) {
	replication := ReplicationConfig{NodeID: hlc.NodeID{0x31}, nodeIDExplicit: true}
	if err := validateReceiptWALConfig(ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}, backup.Config{}, ReplicationConfig{}); err != nil {
		t.Fatalf("graph-only default: %v", err)
	}
	valid := validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "receipts.wal"), ReceiptWALModeFresh)
	if err := validateReceiptWALConfig(valid, backup.Config{}, replication); err != nil {
		t.Fatalf("valid fresh config: %v", err)
	}
	valid.Mode = ReceiptWALModeRestart
	if err := validateReceiptWALConfig(valid, backup.Config{}, replication); err != nil {
		t.Fatalf("valid restart config: %v", err)
	}

	tests := []struct {
		name    string
		config  ReceiptWALConfig
		backups backup.Config
		want    string
	}{
		{
			name:   "unknown mode",
			config: ReceiptWALConfig{Mode: "auto"},
			want:   "must be exactly",
		},
		{
			name: "graph-only mixed settings",
			config: ReceiptWALConfig{
				Mode: ReceiptWALModeGraphOnly,
				Path: filepath.Join(t.TempDir(), "unused.wal"),
			},
			want: "cannot be combined",
		},
		{
			name:   "missing path",
			config: validReceiptWALProviderConfig("", ReceiptWALModeFresh),
			want:   "absolute path",
		},
		{
			name:   "relative path",
			config: validReceiptWALProviderConfig("receipts.wal", ReceiptWALModeFresh),
			want:   "absolute path",
		},
		{
			name: "zero epoch",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "zero.wal"), ReceiptWALModeFresh)
				config.Epoch = mutationreceipt.Epoch{}
				return config
			}(),
			want: "nonzero",
		},
		{
			name: "short retention",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "short.wal"), ReceiptWALModeFresh)
				config.Retention = time.Minute
				return config
			}(),
			want: "policy",
		},
		{
			name: "sub-millisecond retention",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "precision.wal"), ReceiptWALModeFresh)
				config.Retention = time.Hour + time.Nanosecond
				return config
			}(),
			want: "policy",
		},
		{
			name: "zero entries",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "entries.wal"), ReceiptWALModeFresh)
				config.MaxEntries = 0
				return config
			}(),
			want: "policy",
		},
		{
			name: "zero bytes",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "bytes.wal"), ReceiptWALModeFresh)
				config.MaxBytes = 0
				return config
			}(),
			want: "must be positive",
		},
		{
			name:    "required restore disabled",
			config:  validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "restore.wal"), ReceiptWALModeFresh),
			backups: backup.Config{RestoreRequired: true},
			want:    "RESTORE_REQUIRED requires",
		},
	}
	if err := validateReceiptWALConfig(
		validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "backup.wal"), ReceiptWALModeFresh),
		backup.Config{Enabled: true},
		replication,
	); err != nil {
		t.Fatalf("durable receipt backup production: %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReceiptWALConfig(tc.config, tc.backups, replication)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateReceiptWALConfig error = %v, want %q", err, tc.want)
			}
		})
	}
	valid.Mode = ReceiptWALModeRestart
	if err := validateReceiptWALConfig(valid, backup.Config{}, ReplicationConfig{NodeID: hlc.NodeID{0x31}}); err == nil ||
		!strings.Contains(err.Error(), "explicitly set") {
		t.Fatalf("implicit node ID validation error = %v", err)
	}
	if err := validateReceiptWALConfig(valid, backup.Config{}, ReplicationConfig{nodeIDExplicit: true}); err == nil ||
		!strings.Contains(err.Error(), "nonzero") {
		t.Fatalf("zero node ID validation error = %v", err)
	}
}

func TestLoadReceiptWALConfig(t *testing.T) {
	envconfig.ResetForTesting()
	t.Cleanup(envconfig.ResetForTesting)
	path := filepath.Join(t.TempDir(), "receipts.wal")
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "restart")
	t.Setenv("LANTERN_RECEIPT_WAL_PATH", path)
	t.Setenv("LANTERN_RECEIPT_EPOCH", "42424242424242424242424242424242")
	t.Setenv("LANTERN_RECEIPT_RETENTION", "2h")
	t.Setenv("LANTERN_RECEIPT_MAX_ENTRIES", "37")
	t.Setenv("LANTERN_RECEIPT_MAX_BYTES", "4096")
	t.Setenv("LANTERN_NODE_ID", "31313131313131313131313131313131")
	t.Setenv("LANTERN_BACKUP_ENABLED", "false")
	t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")

	config, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.ReceiptWAL.Mode != ReceiptWALModeRestart ||
		config.ReceiptWAL.Path != path ||
		config.ReceiptWAL.Epoch != (mutationreceipt.Epoch{
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
		}) ||
		config.ReceiptWAL.Retention != 2*time.Hour ||
		config.ReceiptWAL.MaxEntries != 37 ||
		config.ReceiptWAL.MaxBytes != 4096 {
		t.Fatalf("loaded receipt WAL config = %+v", config.ReceiptWAL)
	}

	envconfig.ResetForTesting()
	t.Setenv("LANTERN_RECEIPT_EPOCH", "not-hex")
	if _, err := NewConfig(); err == nil || !strings.Contains(err.Error(), "LANTERN_RECEIPT_EPOCH") {
		t.Fatalf("malformed epoch NewConfig error = %v", err)
	}

	envconfig.ResetForTesting()
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "graph-only")
	t.Setenv("LANTERN_RECEIPT_WAL_PATH", "")
	t.Setenv("LANTERN_RECEIPT_EPOCH", "")
	t.Setenv("LANTERN_RECEIPT_RETENTION", "")
	t.Setenv("LANTERN_RECEIPT_MAX_ENTRIES", "")
	t.Setenv("LANTERN_RECEIPT_MAX_BYTES", "")
	t.Setenv("LANTERN_NODE_ID", "")
	graphOnly, err := NewConfig()
	if err != nil {
		t.Fatalf("graph-only random NodeID fallback: %v", err)
	}
	if graphOnly.Replication.NodeID == (hlc.NodeID{}) || graphOnly.Replication.nodeIDExplicit {
		t.Fatalf("graph-only fallback NodeID = %x, explicit %v", graphOnly.Replication.NodeID, graphOnly.Replication.nodeIDExplicit)
	}
}

func TestNewServingRuntimeSelectsExplicitMode(t *testing.T) {
	cacheConfig := CacheConfig{TTL: time.Hour}
	searchConfig := SearchConfig{Enabled: true, Positions: true}
	logConfig := MutationLogConfig{Capacity: 8, SubscriberBuffer: 2}
	replicationConfig := ReplicationConfig{NodeID: hlc.NodeID{0x51}, nodeIDExplicit: true}

	graphOnly, cleanup, err := NewServingRuntime(
		ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly},
		cacheConfig,
		searchConfig,
		logConfig,
		replicationConfig,
		backup.Config{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || graphOnly.DurableReceiptWAL() {
		t.Fatalf("graph-only runtime = %p, cleanup %v, durable %v", graphOnly, cleanup != nil, graphOnly.DurableReceiptWAL())
	}
	cleanup()
}

func TestNewServingRuntimeFreshRestoreAndNoBackupPolicy(t *testing.T) {
	cacheConfig := CacheConfig{TTL: time.Hour}
	searchConfig := SearchConfig{}
	logConfig := MutationLogConfig{Capacity: 16, SubscriberBuffer: 2}
	node := ReplicationConfig{NodeID: hlc.NodeID{0x51}, nodeIDExplicit: true}
	backupDir := t.TempDir()
	instance := "fresh-restore"
	sourcePath := filepath.Join(t.TempDir(), "source.wal")
	sourceConfig := validReceiptWALProviderConfig(sourcePath, ReceiptWALModeFresh)
	produceProviderReceiptBackup(
		t,
		sourceConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: backupDir, Interval: time.Hour,
			Retain: 1, InstanceID: instance,
		},
		false,
	)

	targetConfig := validReceiptWALProviderConfig(
		filepath.Join(t.TempDir(), "fresh-restored.wal"),
		ReceiptWALModeFresh,
	)
	targetConfig.Epoch[0] ^= 1
	restoreConfig := backup.Config{
		Enabled: true, Dir: backupDir, Interval: time.Hour,
		Retain: 1, InstanceID: instance, RestoreOnStart: true, RestoreRequired: true,
	}
	interruptedConfig := targetConfig
	interruptedConfig.Path = filepath.Join(t.TempDir(), "interrupted-fresh.wal")
	interrupted, interruptedCleanup, err := NewServingRuntime(
		interruptedConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		restoreConfig,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := interrupted.GraphCache().GetVertex("startup-restored"); ok {
		interruptedCleanup()
		t.Fatal("interrupted fresh restore exposed the archived graph")
	}
	interruptedCleanup()
	interruptedConfig.Mode = ReceiptWALModeRestart
	if runtime, cleanup, err := NewServingRuntime(
		interruptedConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{},
		nil,
	); runtime != nil || cleanup != nil ||
		err == nil || !strings.Contains(err.Error(), "requires its committed startup restore baseline") {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("interrupted fresh restore restart = %p, %v, %v", runtime, cleanup != nil, err)
	}

	runtime, cleanup, err := NewServingRuntime(
		targetConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		restoreConfig,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, ok := runtime.GraphCache().GetVertex("startup-restored"); ok {
		t.Fatal("fresh backup graph became visible before the restore barrier")
	}
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	if _, err := NewRuntimeRestored(runtime, primary); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.GraphCache().GetVertex("startup-restored"); !ok {
		t.Fatal("fresh restore did not publish the archived graph")
	}
	if matches, err := filepath.Glob(targetConfig.Path + ".receipt.*.baseline"); err != nil ||
		len(matches) != 1 {
		t.Fatalf("fresh restore baseline sidecars = %v, %v; want one", matches, err)
	}

	optional := validReceiptWALProviderConfig(
		filepath.Join(t.TempDir(), "optional-empty.wal"),
		ReceiptWALModeFresh,
	)
	optionalRuntime, optionalCleanup, err := NewServingRuntime(
		optional,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: t.TempDir(), InstanceID: "missing",
			RestoreOnStart: true,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("optional missing fresh backup: %v", err)
	}
	optionalPrimary := optionalRuntime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	if _, err := NewRuntimeRestored(optionalRuntime, optionalPrimary); err != nil {
		t.Fatal(err)
	}
	optionalCleanup()

	requiredPath := filepath.Join(t.TempDir(), "required-missing.wal")
	required := validReceiptWALProviderConfig(requiredPath, ReceiptWALModeFresh)
	if runtime, cleanup, err := NewServingRuntime(
		required,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: t.TempDir(), InstanceID: "missing",
			RestoreOnStart: true, RestoreRequired: true,
		},
		nil,
	); runtime != nil || cleanup != nil || !errors.Is(err, backup.ErrReceiptBackupSetNotFound) {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("required missing fresh backup = %p, %v, %v", runtime, cleanup != nil, err)
	}
	if _, err := os.Stat(requiredPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("required missing restore created WAL bytes: %v", err)
	}

	archives, err := filepath.Glob(filepath.Join(backupDir, "*.active.lar"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("fresh backup archives = %v, %v", archives, err)
	}
	if err := os.WriteFile(archives[0], []byte("corrupt selected backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(t.TempDir(), "optional-invalid.wal")
	invalid := validReceiptWALProviderConfig(invalidPath, ReceiptWALModeFresh)
	invalid.Epoch[0] ^= 2
	if runtime, cleanup, err := NewServingRuntime(
		invalid,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: backupDir, InstanceID: instance,
			RestoreOnStart: true,
		},
		nil,
	); runtime != nil || cleanup != nil || err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("optional invalid fresh backup = %p, %v, %v", runtime, cleanup != nil, err)
	}
	if _, err := os.Stat(invalidPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("optional invalid restore created WAL bytes: %v", err)
	}
}

func TestNewServingRuntimeRestartFallsBackOnlyForMissingBaseline(t *testing.T) {
	cacheConfig := CacheConfig{TTL: time.Hour}
	searchConfig := SearchConfig{}
	logConfig := MutationLogConfig{Capacity: 16, SubscriberBuffer: 2}
	node := ReplicationConfig{NodeID: hlc.NodeID{0x52}, nodeIDExplicit: true}
	backupDir := t.TempDir()
	instance := "restart-restore"
	path := filepath.Join(t.TempDir(), "restart.wal")
	freshConfig := validReceiptWALProviderConfig(path, ReceiptWALModeFresh)
	produceProviderReceiptBackup(
		t,
		freshConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: backupDir, Interval: time.Hour,
			Retain: 1, InstanceID: instance,
		},
		true,
	)
	sidecars, err := filepath.Glob(path + ".receipt.*.baseline")
	if err != nil || len(sidecars) != 1 {
		t.Fatalf("source baseline sidecars = %v, %v", sidecars, err)
	}
	if err := os.Remove(sidecars[0]); err != nil {
		t.Fatal(err)
	}

	restart := freshConfig
	restart.Mode = ReceiptWALModeRestart
	if runtime, cleanup, err := NewServingRuntime(
		restart,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: t.TempDir(), InstanceID: "missing",
			RestoreOnStart: true,
		},
		nil,
	); runtime != nil || cleanup != nil ||
		!errors.Is(err, backup.ErrReceiptBackupSetNotFound) {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("optional missing restart backup = %p, %v, %v", runtime, cleanup != nil, err)
	}
	restoreConfig := backup.Config{
		Enabled: true, Dir: backupDir, Interval: time.Hour,
		Retain: 1, InstanceID: instance, RestoreOnStart: true, RestoreRequired: true,
	}
	runtime, cleanup, err := NewServingRuntime(
		restart,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		restoreConfig,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	if _, err := NewRuntimeRestored(runtime, primary); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if _, ok := runtime.GraphCache().GetVertex("startup-restored"); !ok {
		cleanup()
		t.Fatal("restart fallback did not publish the archived graph")
	}
	cleanup()

	_, cleanup, err = NewServingRuntime(
		restart,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{},
		nil,
	)
	if err != nil {
		t.Fatalf("restored runtime did not become a normal restart: %v", err)
	}
	cleanup()
}

func TestNewServingRuntimePrefersValidRestartOverInvalidBackup(t *testing.T) {
	cacheConfig := CacheConfig{TTL: time.Hour}
	searchConfig := SearchConfig{}
	logConfig := MutationLogConfig{Capacity: 16, SubscriberBuffer: 2}
	node := ReplicationConfig{NodeID: hlc.NodeID{0x53}, nodeIDExplicit: true}
	backupDir := t.TempDir()
	instance := "prefer-current"
	path := filepath.Join(t.TempDir(), "current.wal")
	freshConfig := validReceiptWALProviderConfig(path, ReceiptWALModeFresh)
	produceProviderReceiptBackup(
		t,
		freshConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: backupDir, Interval: time.Hour,
			Retain: 1, InstanceID: instance,
		},
		false,
	)
	archives, err := filepath.Glob(filepath.Join(backupDir, "*.active.lar"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("backup archives = %v, %v", archives, err)
	}
	if err := os.WriteFile(archives[0], []byte("corrupt newest"), 0o600); err != nil {
		t.Fatal(err)
	}

	restart := freshConfig
	restart.Mode = ReceiptWALModeRestart
	runtime, cleanup, err := NewServingRuntime(
		restart,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{
			Enabled: true, Dir: backupDir, InstanceID: instance,
			RestoreOnStart: true, RestoreRequired: true,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("valid current WAL consulted invalid backup: %v", err)
	}
	defer cleanup()
	if _, ok := runtime.GraphCache().GetVertex("startup-restored"); !ok {
		t.Fatal("valid current WAL lost graph state")
	}
}

func TestNewServingRuntimeRestartRejectsIneligibleFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string) func()
	}{
		{
			name: "lease held",
			mutate: func(t *testing.T, path string) func() {
				t.Helper()
				lease, err := mutationlog.AcquireFileWALLease(path)
				if err != nil {
					t.Fatal(err)
				}
				return func() {
					if err := lease.Close(); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "corrupt WAL",
			mutate: func(t *testing.T, path string) func() {
				t.Helper()
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write([]byte("ambiguous trailing WAL bytes")); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				return func() {}
			},
		},
		{
			name: "corrupt tip journal",
			mutate: func(t *testing.T, path string) func() {
				t.Helper()
				if err := os.WriteFile(path+".tip", []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
				return func() {}
			},
		},
		{
			name: "corrupt clock journal",
			mutate: func(t *testing.T, path string) func() {
				t.Helper()
				if err := os.WriteFile(path+".clock", []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
				return func() {}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cacheConfig := CacheConfig{TTL: time.Hour}
			searchConfig := SearchConfig{}
			logConfig := MutationLogConfig{Capacity: 16, SubscriberBuffer: 2}
			node := ReplicationConfig{NodeID: hlc.NodeID{0x54}, nodeIDExplicit: true}
			backupDir := t.TempDir()
			instance := "ineligible-" + strings.ReplaceAll(tc.name, " ", "-")
			path := filepath.Join(t.TempDir(), "current.wal")
			freshConfig := validReceiptWALProviderConfig(path, ReceiptWALModeFresh)
			produceProviderReceiptBackup(
				t,
				freshConfig,
				cacheConfig,
				searchConfig,
				logConfig,
				node,
				backup.Config{
					Enabled: true, Dir: backupDir, Interval: time.Hour,
					Retain: 1, InstanceID: instance,
				},
				true,
			)
			archives, err := filepath.Glob(filepath.Join(backupDir, "*.active.lar"))
			if err != nil || len(archives) != 1 {
				t.Fatalf("backup archives = %v, %v", archives, err)
			}
			if err := os.WriteFile(archives[0], []byte("must not be read"), 0o600); err != nil {
				t.Fatal(err)
			}
			release := tc.mutate(t, path)
			defer release()

			restart := freshConfig
			restart.Mode = ReceiptWALModeRestart
			runtime, cleanup, err := NewServingRuntime(
				restart,
				cacheConfig,
				searchConfig,
				logConfig,
				node,
				backup.Config{
					Enabled: true, Dir: backupDir, InstanceID: instance,
					RestoreOnStart: true, RestoreRequired: true,
				},
				nil,
			)
			if runtime != nil || cleanup != nil || err == nil {
				if cleanup != nil {
					cleanup()
				}
				t.Fatalf("ineligible fallback = %p, %v, %v", runtime, cleanup != nil, err)
			}
			if strings.Contains(err.Error(), "load restart receipt startup backup") {
				t.Fatalf("ineligible current failure consulted backup: %v", err)
			}
		})
	}
}

func produceProviderReceiptBackup(
	t *testing.T,
	config ReceiptWALConfig,
	cacheConfig CacheConfig,
	searchConfig SearchConfig,
	logConfig MutationLogConfig,
	node ReplicationConfig,
	backupConfig backup.Config,
	installBaseline bool,
) {
	t.Helper()
	runtime, cleanup, err := NewServingRuntime(
		config,
		cacheConfig,
		searchConfig,
		logConfig,
		node,
		backup.Config{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(runtime, primary)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	certified, err := NewRuntimeCertified(runtime, primary, replication, restored)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if _, err := primary.PutVertex(context.Background(), &pb.PutVertexRequest{
		Vertex: &pb.Vertex{
			Key:        "startup-restored",
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		},
	}); err != nil {
		cleanup()
		t.Fatal(err)
	}
	source, policy, err := runtime.ReceiptWholeStateBackupSource(
		primary,
		certified.replication,
	)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if installBaseline {
		capture, err := source.Capture(context.Background(), policy)
		if err != nil {
			cleanup()
			t.Fatal(err)
		}
		if err := primary.InstallReceiptBaseline(context.Background(), capture); err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	backupper, err := backup.NewReceipt(primary, source, policy, backupConfig, nil, nil)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if _, err := backupper.BackupNow(context.Background()); err != nil {
		cleanup()
		t.Fatal(err)
	}
	cleanup()
}

func TestNewServingRuntimeSelectsDurableModes(t *testing.T) {
	cacheConfig := CacheConfig{TTL: time.Hour}
	searchConfig := SearchConfig{Enabled: true, Positions: true}
	logConfig := MutationLogConfig{Capacity: 8, SubscriberBuffer: 2}
	replicationConfig := ReplicationConfig{NodeID: hlc.NodeID{0x51}, nodeIDExplicit: true}
	path := filepath.Join(t.TempDir(), "receipts.wal")
	freshConfig := validReceiptWALProviderConfig(path, ReceiptWALModeFresh)
	implicitNode := replicationConfig
	implicitNode.nodeIDExplicit = false
	if runtime, cleanup, err := NewServingRuntime(
		freshConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		implicitNode,
		backup.Config{},
		nil,
	); runtime != nil || cleanup != nil || err == nil || !strings.Contains(err.Error(), "LANTERN_NODE_ID") {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("implicit durable node ID = %p, %v, %v", runtime, cleanup != nil, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("implicit durable node ID created WAL bytes: %v", err)
	}
	fresh, cleanupFresh, err := NewServingRuntime(
		freshConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		replicationConfig,
		backup.Config{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleanupFresh == nil || !fresh.DurableReceiptWAL() {
		t.Fatalf("fresh runtime = %p, cleanup %v, durable %v", fresh, cleanupFresh != nil, fresh.DurableReceiptWAL())
	}
	cleanupFresh()

	restartConfig := freshConfig
	restartConfig.Mode = ReceiptWALModeRestart
	restarted, cleanupRestart, err := NewServingRuntime(
		restartConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		replicationConfig,
		backup.Config{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleanupRestart == nil || !restarted.DurableReceiptWAL() {
		t.Fatalf("restart runtime = %p, cleanup %v, durable %v", restarted, cleanupRestart != nil, restarted.DurableReceiptWAL())
	}
	cleanupRestart()

	if runtime, cleanup, err := NewServingRuntime(
		freshConfig,
		cacheConfig,
		searchConfig,
		logConfig,
		replicationConfig,
		backup.Config{},
		nil,
	); runtime != nil || cleanup != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("fresh reused existing generation = %p, %v, %v", runtime, cleanup != nil, err)
	}
}

func TestRuntimeCertificationRejectsIncompleteServices(t *testing.T) {
	certified, err := NewRuntimeCertified(nil, nil, nil, runtimeRestored{})
	if certified.valid || err == nil {
		t.Fatalf("incomplete runtime certification = %+v, %v", certified, err)
	}
	listener, cleanup, err := NewListener(NetConfig{}, certified)
	if listener != nil || cleanup != nil || err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("uncertified listener = %v, %v, %v", listener, cleanup != nil, err)
	}

	newGraphOnly := func(t *testing.T) (*service.ServingRuntime, func()) {
		t.Helper()
		runtime, cleanup, err := NewServingRuntime(
			ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly},
			CacheConfig{TTL: time.Hour},
			SearchConfig{},
			MutationLogConfig{Capacity: 8},
			ReplicationConfig{NodeID: hlc.NodeID{0x61}},
			backup.Config{},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		return runtime, cleanup
	}
	first, cleanupFirst := newGraphOnly(t)
	defer cleanupFirst()
	firstPrimary := first.NewLanternService(nil)
	firstReplication, err := first.NewLanternReplicationService(firstPrimary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(first, firstPrimary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err = NewRuntimeCertified(first, firstPrimary, firstReplication, restored)
	if err != nil || !certified.valid {
		t.Fatalf("valid runtime certification = %+v, %v", certified, err)
	}

	second, cleanupSecond := newGraphOnly(t)
	defer cleanupSecond()
	secondPrimary := second.NewLanternService(nil)
	secondReplication, err := second.NewLanternReplicationService(secondPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if certified, err := NewRuntimeCertified(first, secondPrimary, secondReplication, restored); certified.valid || err == nil {
		t.Fatalf("mismatched runtime certification = %+v, %v", certified, err)
	}
}
