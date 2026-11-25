package provider

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func validReceiptWALProviderConfig(mode ReceiptWALMode) ReceiptWALConfig {
	return ReceiptWALConfig{
		Mode:       mode,
		Epoch:      mutationreceipt.Epoch{0x42},
		Retention:  time.Hour,
		MaxEntries: 32,
		MaxBytes:   1 << 20,
	}
}

func TestOIDCPublicReceiptsCertificationRequiresExactManagedBackupper(t *testing.T) {
	data := securityDataFixture(t)
	primary := data.NewExecutor(nil)
	peer, err := data.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, primary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewRuntimeCertified(data, primary, peer, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, cleanup, err := NewSecurityRuntime(t.Context(), SecurityConfig{Mode: "off"}, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	logOwner := newServingLogger(ObservabilityConfig{}, io.Discard)
	logging, err := NewManagedLogRuntime(t.Context(), runtime, logOwner)
	if err != nil {
		t.Fatal(err)
	}
	operational := operationalPublicTestCertification(t, runtime, primary, NewSlowRPCInterceptor(0, logOwner.logger), certified)
	public, err := NewPublicSecurityCertified(runtime, TLSConfig{}, nil, data, primary, certified, logging, operational)
	if err != nil {
		t.Fatal(err)
	}
	config := ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}
	if _, err := NewOIDCPublicReceiptsCertified(config, runtime, data, primary, operational.backup, certified, public); err != nil {
		t.Fatal("exact certified producer refused", err)
	}
	for name, owner := range map[string]*backup.Backupper{
		"absent":                            nil,
		"same executor, different producer": backup.New(primary, backup.Config{Interval: time.Hour}, nil, nil),
		"foreign executor":                  backup.New(data.NewExecutor(nil), backup.Config{Interval: time.Hour}, nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewOIDCPublicReceiptsCertified(config, runtime, data, primary, owner, certified, public); err == nil {
				t.Fatal("uncertified producer reached receipt activation")
			}
		})
	}
}

func TestValidateReceiptWALConfig(t *testing.T) {
	replication := ReplicationConfig{NodeID: hlc.NodeID{0x31}, nodeIDExplicit: true}
	if err := validateReceiptWALConfig(ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}, backup.Config{}, ReplicationConfig{}); err != nil {
		t.Fatalf("graph-only default: %v", err)
	}
	valid := validReceiptWALProviderConfig(ReceiptWALModeFresh)
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
				Mode:      ReceiptWALModeGraphOnly,
				Retention: time.Hour,
			},
			want: "cannot be combined",
		},
		{
			name: "zero epoch",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(ReceiptWALModeFresh)
				config.Epoch = mutationreceipt.Epoch{}
				return config
			}(),
			want: "nonzero",
		},
		{
			name: "short retention",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(ReceiptWALModeFresh)
				config.Retention = time.Minute
				return config
			}(),
			want: "policy",
		},
		{
			name: "sub-millisecond retention",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(ReceiptWALModeFresh)
				config.Retention = time.Hour + time.Nanosecond
				return config
			}(),
			want: "policy",
		},
		{
			name: "zero entries",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(ReceiptWALModeFresh)
				config.MaxEntries = 0
				return config
			}(),
			want: "policy",
		},
		{
			name: "zero bytes",
			config: func() ReceiptWALConfig {
				config := validReceiptWALProviderConfig(ReceiptWALModeFresh)
				config.MaxBytes = 0
				return config
			}(),
			want: "must be positive",
		},
		{
			name:    "required restore disabled",
			config:  validReceiptWALProviderConfig(ReceiptWALModeFresh),
			backups: backup.Config{RestoreRequired: true},
			want:    "RESTORE_REQUIRED requires",
		},
	}
	if err := validateReceiptWALConfig(
		validReceiptWALProviderConfig(ReceiptWALModeFresh),
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

	path := filepath.Join(t.TempDir(), "receipts.wal")
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "restart")
	t.Setenv("LANTERN_DATA_DIR", path)
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
		config.Cache.DataDir != path ||
		config.ReceiptWAL.Epoch != (mutationreceipt.Epoch{
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
		}) ||
		config.ReceiptWAL.Retention != 2*time.Hour ||
		config.ReceiptWAL.MaxEntries != 37 ||
		config.ReceiptWAL.MaxBytes != 4096 {
		t.Fatalf("loaded receipt WAL config = %+v", config.ReceiptWAL)
	}

	t.Setenv("LANTERN_RECEIPT_EPOCH", "not-hex")
	if _, err := NewConfig(); err == nil || !strings.Contains(err.Error(), "LANTERN_RECEIPT_EPOCH") {
		t.Fatalf("malformed epoch NewConfig error = %v", err)
	}

	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "graph-only")
	t.Setenv("LANTERN_DATA_DIR", "")
	t.Setenv("LANTERN_RECEIPT_EPOCH", "")
	t.Setenv("LANTERN_RECEIPT_RETENTION", "")
	t.Setenv("LANTERN_RECEIPT_MAX_ENTRIES", "")
	t.Setenv("LANTERN_RECEIPT_MAX_BYTES", "")
	t.Setenv("LANTERN_NODE_ID", "")
	graphOnly, err := NewConfig()
	if err != nil {
		t.Fatalf("graph-only random NodeID fallback: %v", err)
	}
	if graphOnly.Replication.NodeID != (hlc.NodeID{}) || graphOnly.Replication.nodeIDExplicit {
		t.Fatalf("graph-only fallback NodeID = %x, explicit %v", graphOnly.Replication.NodeID, graphOnly.Replication.nodeIDExplicit)
	}
}

func TestNewServingRuntimeSelectsExplicitMode(t *testing.T) {
	cacheConfig := CacheConfig{CacheSettings: CacheSettings{DataDir: filepath.Join(t.TempDir(), "graph"), TTL: time.Hour}}
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

func TestNewServingRuntimeSelectsDurableModes(t *testing.T) {
	cacheConfig := CacheConfig{CacheSettings: CacheSettings{DataDir: filepath.Join(t.TempDir(), "graph"), TTL: time.Hour}}
	searchConfig := SearchConfig{Enabled: true, Positions: true}
	logConfig := MutationLogConfig{Capacity: 8, SubscriberBuffer: 2}
	replicationConfig := ReplicationConfig{NodeID: hlc.NodeID{0x51}, nodeIDExplicit: true}
	path := cacheConfig.DataDir
	freshConfig := validReceiptWALProviderConfig(ReceiptWALModeFresh)
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
	certified, err := NewRuntimeCertified(nil, nil, nil, runtimeRestored{}, NetConfig{})
	if certified.valid || err == nil {
		t.Fatalf("incomplete runtime certification = %+v, %v", certified, err)
	}
	listener, cleanup, err := NewListener(NetConfig{}, certified, publicReceiptsCertified{}, nil)
	if listener != nil || cleanup != nil || err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("uncertified listener = %v, %v, %v", listener, cleanup != nil, err)
	}

	newGraphOnly := func(t *testing.T) (*dataruntime.ServingRuntime, func()) {
		t.Helper()
		runtime, cleanup, err := NewServingRuntime(
			ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly},
			CacheConfig{CacheSettings: CacheSettings{DataDir: t.TempDir(), TTL: time.Hour}},
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
	firstPrimary := first.NewExecutor(nil)
	firstReplication, err := first.NewReplication(firstPrimary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(first, firstPrimary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err = NewRuntimeCertified(first, firstPrimary, firstReplication, restored, NetConfig{})
	if err != nil || !certified.valid {
		t.Fatalf("valid runtime certification = %+v, %v", certified, err)
	}

	second, cleanupSecond := newGraphOnly(t)
	defer cleanupSecond()
	secondPrimary := second.NewExecutor(nil)
	secondReplication, err := second.NewReplication(secondPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if certified, err := NewRuntimeCertified(first, secondPrimary, secondReplication, restored, NetConfig{}); certified.valid || err == nil {
		t.Fatalf("mismatched runtime certification = %+v, %v", certified, err)
	}
}
