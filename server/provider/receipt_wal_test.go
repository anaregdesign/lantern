package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/service"
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
			name:    "backup producer",
			config:  validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "backup.wal"), ReceiptWALModeFresh),
			backups: backup.Config{Enabled: true},
			want:    "must be disabled",
		},
		{
			name:    "legacy restore",
			config:  validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "restore.wal"), ReceiptWALModeFresh),
			backups: backup.Config{RestoreOnStart: true},
			want:    "must be disabled",
		},
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
	certified, err := NewRuntimeCertified(nil, nil, nil)
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
	certified, err = NewRuntimeCertified(first, firstPrimary, firstReplication)
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
	if certified, err := NewRuntimeCertified(first, secondPrimary, secondReplication); certified.valid || err == nil {
		t.Fatalf("mismatched runtime certification = %+v, %v", certified, err)
	}
}
