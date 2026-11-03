package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

func managedBackupTestOwner(t *testing.T, input ...string) (*Config, *backup.Backupper, *prometheus.Registry) {
	t.Helper()
	config, err := LoadConfig(envconfig.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly})
	config.Backup.NamespaceFormat = runtime.DataNamespaceFormat()
	if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "preserved"}}); err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	owner, err := NewBackupper(config.Backup, ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}, runtime, primary, certified, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	return config, owner, registry
}

func managedBackupTestManager(t *testing.T, config *Config) *ManagedOperationalRuntime {
	t.Helper()
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off"}, NewOperationalBootstrapConfig(config))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestManagedBackupBootstrapProvenanceAndActualObservation(t *testing.T) {
	config, owner, _ := managedBackupTestOwner(t, "LANTERN_BACKUP_ENABLED=true")
	manager := managedBackupTestManager(t, config)
	certified, err := NewManagedBackupRuntime(t.Context(), manager, owner)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.Effective == nil || actual.Effective.Backup.Enabled || actual.EffectiveGeneration != 0 || actual.Fingerprint != "" ||
		actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_ENABLED] != securitytransport.OperationalSourceDerived ||
		actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_INTERVAL_NANOS] != securitytransport.OperationalSourceDefault {
		t.Fatalf("bootstrap observation = %+v, %v", actual, err)
	}
	actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_ENABLED] = securitytransport.OperationalSourceManaged
	again, _ := certified.adapter.ObserveOperationalPolicy()
	if again.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_ENABLED] != securitytransport.OperationalSourceDerived {
		t.Fatal("observation source aliases bootstrap")
	}
	if certified.adapter.AvailableOperationalPolicy(operationalpolicy.GroupCapacity, 1) == nil || certified.adapter.AvailableOperationalPolicy(operationalpolicy.GroupBackup, 2) == nil {
		t.Fatal("unsupported group/schema advertised")
	}
	if _, err := NewManagedBackupRuntime(t.Context(), manager, owner); err == nil {
		t.Fatal("duplicate group owner accepted")
	}
	if _, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, &Config{}), owner); err == nil {
		t.Fatal("uncaptured metadata invented provenance")
	}
	if _, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, config), &backup.Backupper{}); err == nil {
		t.Fatal("unconstructed backup owner accepted")
	}
}

func TestManagedBackupPrepareApplyPreviewAndRestoreOwnership(t *testing.T) {
	dir := t.TempDir()
	config, owner, registry := managedBackupTestOwner(t, "LANTERN_BACKUP_DIR="+dir, "LANTERN_BACKUP_ENABLED=true", "LANTERN_BACKUP_RETAIN=0", "LANTERN_BACKUP_INTERVAL=1000000007ns")
	certified, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := owner.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	values := operationalpolicy.Values{Backup: &operationalpolicy.Backup{Enabled: false, IntervalNanos: 1000000013, Retain: 1}}
	change := operationalpolicy.Change{Values: values, RetentionReductionAcknowledged: true}
	if err := certified.adapter.PrepareOperationalPolicy(change); err != nil {
		t.Fatal(err)
	}
	preview, err := NewManagedBackupPreviewer(certified)(t.Context(), 1)
	if err != nil || !preview.Complete || preview.ValidBackups != 2 || preview.PruneBackups != 1 || len(preview.Fingerprint) != 64 {
		t.Fatalf("connected preview = %+v, %v", preview, err)
	}
	policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}
	if err := certified.adapter.ApplyOperationalPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Fatal("prepare/preview/apply deleted recovery files")
	}
	actual, _ := certified.adapter.ObserveOperationalPolicy()
	if actual.EffectiveGeneration != 1 || actual.Effective.Backup.IntervalNanos != values.Backup.IntervalNanos || actual.Fingerprint != "" {
		t.Fatalf("installed observation = %+v", actual)
	}
	for _, source := range actual.Sources {
		if source != securitytransport.OperationalSourceManaged {
			t.Fatal("managed generation retained env/default source")
		}
	}
	if _, err := owner.RestoreOnStartup(t.Context()); err != nil {
		t.Fatalf("managed production disable changed bootstrap restoration: %v", err)
	}
	// Existing newer HLC floors may legitimately supersede restored values;
	// the completed restore timestamp proves the bootstrap path still ran.
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	for _, metric := range metrics {
		if metric.GetName() == "lantern_restore_last_timestamp_seconds" && metric.GetMetric()[0].GetGauge().GetValue() > 0 {
			restored = true
		}
	}
	if !restored {
		t.Fatal("managed production disable suppressed bootstrap restoration")
	}
	if _, err := owner.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	preview, err = NewManagedBackupPreviewer(certified)(t.Context(), 1)
	if err != nil || preview.ValidBackups != 1 {
		t.Fatalf("future-success retention = %+v, %v", preview, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := owner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	policy.Generation++
	if err := certified.adapter.ApplyOperationalPolicy(policy); err == nil {
		t.Fatal("stopped owner applied next generation")
	}
	actual, _ = certified.adapter.ObserveOperationalPolicy()
	if actual.EffectiveGeneration != 1 || actual.Error != securitytransport.OperationalErrorOwnerUnavailable {
		t.Fatalf("stopped observation lost installed generation: %+v", actual)
	}
}

func TestManagedBackupRecoveredPolicyAndNoAuthorityCallbackReads(t *testing.T) {
	config, owner, _ := managedBackupTestOwner(t, "LANTERN_BACKUP_DIR="+t.TempDir())
	projection := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(1), operationalpolicy.GroupBackup)
	policy := &operationalpolicy.Policy{Version: 1, Generation: 3, Values: operationalpolicy.Values{Backup: &operationalpolicy.Backup{Enabled: false, IntervalNanos: 3000000007, Retain: 0}}}
	if err := projection.Policies.Set(operationalpolicy.GroupBackup, policy); err != nil {
		t.Fatal(err)
	}
	manager, authority := operationalRuntimeTestManager(t, projection)
	manager.bootstrap = NewOperationalBootstrapConfig(config)
	certified, err := NewManagedBackupRuntime(t.Context(), manager, owner)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Production().Generation != 3 || authority.binds != 1 {
		t.Fatal("binding preceded recovered actual installation")
	}
	reads := authority.reads
	if err := certified.adapter.PrepareOperationalPolicy(operationalpolicy.Change{ExpectedGeneration: 3, Values: policy.Values}); err != nil {
		t.Fatal(err)
	}
	if _, err := certified.adapter.ObserveOperationalPolicy(); err != nil {
		t.Fatal(err)
	}
	if authority.reads != reads {
		t.Fatal("owner callbacks recursively read native authority")
	}
	authority.mu.Lock()
	authority.readErr = errors.New("unavailable")
	authority.mu.Unlock()
	if manager.Reconcile(t.Context()) == nil {
		t.Fatal("authority failure silently succeeded")
	}
	if owner.Production().Generation != 3 || owner.Production().Config.Interval != time.Duration(policy.Values.Backup.IntervalNanos) {
		t.Fatal("authority failure reverted to environment")
	}
}

func TestManagedBackupImmutableDestinationGuardAndPrivatePreviewErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "private-destination")
	if err := os.WriteFile(file, []byte("owned fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, owner, registry := managedBackupTestOwner(t, "LANTERN_BACKUP_DIR="+file, "LANTERN_BACKUP_ENABLED=true")
	certified, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.Effective == nil || actual.Error != securitytransport.OperationalErrorNone {
		t.Fatalf("installed policy observation = %+v, %v", actual, err)
	}
	change := operationalpolicy.Change{Values: operationalpolicy.Values{Backup: &operationalpolicy.Backup{Enabled: true, IntervalNanos: int64(time.Second), Retain: 0}}}
	if err := certified.adapter.PrepareOperationalPolicy(change); err != nil {
		t.Fatalf("pure preparation must not turn mutable filesystem health into authority: %v", err)
	}
	if _, err := NewManagedBackupPreviewer(certified)(t.Context(), 0); err == nil || strings.Contains(err.Error(), file) {
		t.Fatalf("private path exposed in preview error: %v", err)
	}
	if owner.Production().Generation != 0 {
		t.Fatal("refused preparation changed generation")
	}
	// The explicit future attempt, outside native callbacks, reports actual IO
	// failure through its established error and failure counter.
	if _, err := owner.BackupNow(t.Context()); err == nil {
		t.Fatal("backup attempt hid filesystem failure")
	}
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, metric := range metrics {
		if metric.GetName() == "lantern_backup_failures_total" && metric.GetMetric()[0].GetCounter().GetValue() > 0 {
			failed = true
		}
	}
	if !failed {
		t.Fatal("actual production failure was not observable")
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != "owned fixture" {
		t.Fatal("prepare/observe/failed attempt modified destination fixture")
	}
	for _, bad := range []operationalpolicy.Values{{Backup: &operationalpolicy.Backup{IntervalNanos: 0}}, {Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: 1}}} {
		if _, err := managedBackupConfig(bad); err == nil {
			t.Fatal("wrong/invalid policy converted")
		}
	}
}

func TestManagedBackupPreviewLimitIsIncompleteWithoutPaths(t *testing.T) {
	dir := t.TempDir()
	config, owner, _ := managedBackupTestOwner(t, "LANTERN_BACKUP_DIR="+dir)
	certified, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("lantern-backup-%s-%d.lbk", config.Backup.InstanceID, time.Now().UnixNano()))
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(64 << 20)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	preview, err := NewManagedBackupPreviewer(certified)(t.Context(), 1)
	if err != nil || preview.Complete || preview.Fingerprint != "" {
		t.Fatalf("bounded sample = %+v, %v", preview, err)
	}
	if err := certified.adapter.PrepareOperationalPolicy(operationalpolicy.Change{Values: operationalpolicy.Values{Backup: &operationalpolicy.Backup{IntervalNanos: int64(time.Second), Retain: 0}}}); err != nil {
		t.Fatal("safe keep-all preparation blocked by optional preview", err)
	}
}

func TestManagedBackupInvalidBootstrapIsVisibleAndRepairable(t *testing.T) {
	config, owner, _ := managedBackupTestOwner(t, "LANTERN_BACKUP_INTERVAL=0s")
	certified, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.Effective != nil || actual.Error != securitytransport.OperationalErrorInvalidPolicy || actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_BACKUP_INTERVAL_NANOS] != securitytransport.OperationalSourceEnvironment {
		t.Fatalf("invalid bootstrap = %+v, %v", actual, err)
	}
	if err := certified.adapter.ApplyOperationalPolicy(operationalpolicy.Policy{Version: 1, Generation: 1, Values: operationalpolicy.Values{Backup: &operationalpolicy.Backup{IntervalNanos: 7, Retain: 0}}}); err != nil {
		t.Fatal(err)
	}
	actual, err = certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.Effective == nil || actual.Effective.Backup.IntervalNanos != 7 || actual.Error != securitytransport.OperationalErrorNone || actual.EffectiveGeneration != 1 {
		t.Fatalf("repair = %+v, %v", actual, err)
	}
}

func TestManagedBackupRecoveredEnabledPolicyRefusesMissingExternalDestination(t *testing.T) {
	config, owner, _ := managedBackupTestOwner(t)
	projection := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(1), operationalpolicy.GroupBackup)
	policy := &operationalpolicy.Policy{Version: 1, Generation: 2, Values: operationalpolicy.Values{Backup: &operationalpolicy.Backup{Enabled: true, IntervalNanos: int64(time.Second), Retain: 0}}}
	if err := projection.Policies.Set(operationalpolicy.GroupBackup, policy); err != nil {
		t.Fatal(err)
	}
	manager, authority := operationalRuntimeTestManager(t, projection)
	manager.bootstrap = NewOperationalBootstrapConfig(config)
	if _, err := NewManagedBackupRuntime(t.Context(), manager, owner); err == nil || authority.binds != 0 || owner.Production().Generation != 0 {
		t.Fatal("missing external dependency was bound or claimed restored", err)
	}
}

func TestManagedBackupEnvironmentDisabledProductionRestoresOnNextBoot(t *testing.T) {
	dir := t.TempDir()
	config, owner, _ := managedBackupTestOwner(t, "LANTERN_BACKUP_DIR="+dir, "LANTERN_BACKUP_ENABLED=false")
	if config.Backup.Enabled || !config.Backup.RestoreOnStart {
		t.Fatal("external restoration still depends on production enablement")
	}
	certified, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: operationalpolicy.Values{Backup: &operationalpolicy.Backup{Enabled: true, IntervalNanos: int64(time.Hour), Retain: 0}}}
	if err := certified.adapter.ApplyOperationalPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if snapshot := owner.Production(); !snapshot.Config.Enabled || snapshot.Generation != 1 {
		t.Fatal("managed production did not enable")
	}
	if stats, err := owner.BackupNow(t.Context()); err != nil || stats.Vertices != 1 {
		t.Fatalf("managed production backup = %+v, %v", stats, err)
	}
	for _, test := range []struct {
		name    string
		flag    []string
		restore bool
	}{
		{"default external restore", nil, true},
		{"explicit empty startup", []string{"LANTERN_BACKUP_RESTORE_ON_START=false"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := append([]string{"LANTERN_BACKUP_DIR=" + dir, "LANTERN_BACKUP_ENABLED=false"}, test.flag...)
			restartedConfig, err := LoadConfig(envconfig.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			data, primary, certification := certifiedSnapshotInstallerRuntime(t, ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly})
			restartedConfig.Backup.NamespaceFormat = data.DataNamespaceFormat()
			restartedOwner, err := NewBackupper(restartedConfig.Backup, ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}, data, primary, certification, prometheus.NewRegistry(), nil)
			if err != nil {
				t.Fatal(err)
			}
			// Restore uses external bootstrap selection before the managed
			// registry/authority exists on this restarted instance.
			stats, err := restartedOwner.RestoreOnStartup(t.Context())
			_, present := data.GraphCache().GetVertex("preserved")
			if err != nil || present != test.restore || (stats.Vertices == 1) != test.restore {
				t.Fatalf("independent restoration = %+v, present %v, %v", stats, present, err)
			}
			restarted, err := NewManagedBackupRuntime(t.Context(), managedBackupTestManager(t, restartedConfig), restartedOwner)
			if err != nil {
				t.Fatal(err)
			}
			policy.Generation = 2
			policy.Values.Backup.Enabled = false
			if err := restarted.adapter.ApplyOperationalPolicy(policy); err != nil {
				t.Fatal(err)
			}
			_, present = data.GraphCache().GetVertex("preserved")
			if present != test.restore || restartedOwner.Production().Config.Enabled {
				t.Fatal("managed disable changed selected restoration or restored graph")
			}
		})
	}
}
