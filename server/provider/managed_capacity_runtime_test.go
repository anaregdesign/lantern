package provider

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

func managedCapacityTestOwner(t *testing.T, input ...string) (*Config, *dataruntime.GraphPolicyOwner, *dataruntime.ServingRuntime, *dataruntime.Executor) {
	t.Helper()
	config, err := LoadConfig(envconfig.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly})
	if _, err := primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "restored-a"}, {Key: "restored-b"}}}); err != nil {
		t.Fatal(err)
	}
	owner, err := NewGraphPolicyOwner(config.Cache, runtime, primary, certified)
	if err != nil {
		t.Fatal(err)
	}
	return config, owner, runtime, primary
}

func managedCapacityTestManager(t *testing.T, config *Config) *ManagedOperationalRuntime {
	t.Helper()
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off"}, NewOperationalBootstrapConfig(config))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestManagedCapacityBootstrapProvenanceAndTypedConversion(t *testing.T) {
	config, owner, _, _ := managedCapacityTestOwner(t, "LANTERN_GC_INTERVAL_SECONDS=2", "LANTERN_MAX_VERTICES=7")
	certified, err := NewManagedCapacityRuntime(t.Context(), managedCapacityTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.Effective == nil || actual.EffectiveGeneration != 0 || actual.Fingerprint != "" || actual.Mode != securitytransport.OperationalModeWorkerRestart ||
		actual.Effective.Capacity.GCIntervalSeconds != 2 || actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_CAPACITY_GC_INTERVAL_SECONDS] != securitytransport.OperationalSourceEnvironment ||
		actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_CAPACITY_MAX_EDGES] != securitytransport.OperationalSourceDefault {
		t.Fatalf("bootstrap = %+v, %v", actual, err)
	}
	actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_CAPACITY_MAX_EDGES] = securitytransport.OperationalSourceManaged
	again, _ := certified.adapter.ObserveOperationalPolicy()
	if again.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_CAPACITY_MAX_EDGES] != securitytransport.OperationalSourceDefault {
		t.Fatal("observer mutated bootstrap source metadata")
	}
	if certified.adapter.AvailableOperationalPolicy(operationalpolicy.GroupBackup, 1) == nil || certified.adapter.AvailableOperationalPolicy(operationalpolicy.GroupCapacity, 2) == nil {
		t.Fatal("wrong group/version advertised")
	}
	if _, err := NewManagedCapacityRuntime(t.Context(), managedCapacityTestManager(t, &Config{}), owner); err == nil {
		t.Fatal("uncaptured provenance invented defaults")
	}
	if _, err := NewManagedCapacityRuntime(t.Context(), managedCapacityTestManager(t, config), &dataruntime.GraphPolicyOwner{}); err == nil {
		t.Fatal("unconstructed runtime owner accepted")
	}
	for _, values := range []operationalpolicy.Values{
		{Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: 0}},
		{Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: math.MaxInt64/int64(time.Second) + 1}},
		{Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: 1, MaxVertices: -1}},
		{Backup: &operationalpolicy.Backup{IntervalNanos: 1}},
	} {
		if _, err := managedCapacityConfig(values); err == nil {
			t.Fatal("invalid/wrong group conversion accepted", values)
		}
	}
	maximum := int64(^uint(0) >> 1)
	converted, err := managedCapacityConfig(operationalpolicy.Values{Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: math.MaxInt64 / int64(time.Second), MaxVertices: maximum, MaxEdges: maximum}})
	if err != nil || converted.MaxVertices != int(maximum) || converted.GCInterval <= 0 {
		t.Fatalf("largest representable conversion = %+v, %v", converted, err)
	}
}

func TestManagedCapacityPrepareApplyDoesNotEvictAndReportsActualGeneration(t *testing.T) {
	config, owner, runtime, _ := managedCapacityTestOwner(t)
	certified, err := NewManagedCapacityRuntime(t.Context(), managedCapacityTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	values := operationalpolicy.Values{Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: 2, GCEdgeBudget: 1, MaxVertices: 1, MaxEdges: 1, MaxVertexCausalEntries: 1, MaxEdgeCausalEntries: 1}}
	before := owner.Snapshot()
	if err := certified.adapter.PrepareOperationalPolicy(operationalpolicy.Change{Values: values}); err != nil {
		t.Fatal(err)
	}
	if owner.Snapshot() != before {
		t.Fatal("Prepare mutated effective policy")
	}
	policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}
	if err := certified.adapter.ApplyOperationalPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if runtime.GraphCache().VertexCount() != 2 {
		t.Fatal("managed shrink evicted restored state")
	}
	preview, err := owner.Preview(owner.Snapshot().Config)
	if err != nil || !preview.VerticesOverLimit || !preview.VertexCausalOverLimit {
		t.Fatalf("actual usage/shrink preview = %+v, %v", preview, err)
	}
	actual, err := certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.EffectiveGeneration != 1 || actual.Effective.Capacity.MaxVertices != 1 || actual.Fingerprint != "" {
		t.Fatalf("actual = %+v, %v", actual, err)
	}
	for _, source := range actual.Sources {
		if source != securitytransport.OperationalSourceManaged {
			t.Fatal("managed generation retained bootstrap source")
		}
	}
	if err := certified.adapter.ApplyOperationalPolicy(policy); err != nil {
		t.Fatalf("idempotent committed apply = %v", err)
	}
	policy.Values.Capacity.MaxVertices = 2
	if err := certified.adapter.ApplyOperationalPolicy(policy); err == nil {
		t.Fatal("same generation with different values installed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := owner.RunGC(ctx); err != nil {
		t.Fatal(err)
	}
	policy.Generation++
	if err := certified.adapter.ApplyOperationalPolicy(policy); err == nil {
		t.Fatal("stopped owner applied newer generation")
	}
	actual, err = certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.EffectiveGeneration != 1 || actual.Effective.Capacity.MaxVertices != 1 || actual.Error != securitytransport.OperationalErrorOwnerUnavailable {
		t.Fatalf("stopped owner lost actual generation: %+v, %v", actual, err)
	}
}

func TestManagedCapacityRecoveredPolicyAndAuthorityFailureDoesNotFallback(t *testing.T) {
	config, owner, _, _ := managedCapacityTestOwner(t, "LANTERN_MAX_VERTICES=7")
	projection := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(1), operationalpolicy.GroupCapacity)
	policy := &operationalpolicy.Policy{Version: 1, Generation: 3, Values: operationalpolicy.Values{Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: 3, MaxVertices: 1, MaxVertexCausalEntries: 1}}}
	if err := projection.Policies.Set(operationalpolicy.GroupCapacity, policy); err != nil {
		t.Fatal(err)
	}
	manager, authority := operationalRuntimeTestManager(t, projection)
	manager.bootstrap = NewOperationalBootstrapConfig(config)
	certified, err := NewManagedCapacityRuntime(t.Context(), manager, owner)
	if err != nil {
		t.Fatal(err)
	}
	if authority.binds != 1 || owner.Snapshot().Generation != 3 || owner.Snapshot().Config.MaxVertices != 1 {
		t.Fatal("recovered desired was not installed before binding")
	}
	reads := authority.reads
	if err := certified.adapter.PrepareOperationalPolicy(operationalpolicy.Change{ExpectedGeneration: 3, Values: policy.Values}); err != nil {
		t.Fatal(err)
	}
	if _, err := certified.adapter.ObserveOperationalPolicy(); err != nil {
		t.Fatal(err)
	}
	if authority.reads != reads {
		t.Fatal("resource callback read authority recursively")
	}
	authority.mu.Lock()
	authority.readErr = errors.New("unavailable")
	authority.mu.Unlock()
	if manager.Reconcile(t.Context()) == nil {
		t.Fatal("failed authority read accepted")
	}
	if owner.Snapshot().Generation != 3 || owner.Snapshot().Config.MaxVertices != 1 {
		t.Fatal("authority error fell back to bootstrap cap")
	}
}

func TestManagedCapacityInvalidOverflowedBootstrapRemainsVisibleAndRepairable(t *testing.T) {
	// The legacy loader's unchecked seconds multiplication can retain a
	// positive fractional duration after overflow. Never round it into a
	// seemingly valid effective native value or lose its environment source.
	config, owner, _, _ := managedCapacityTestOwner(t, "LANTERN_GC_INTERVAL_SECONDS=18446744074")
	if owner.Snapshot().Config.GCInterval <= 0 || owner.Snapshot().Config.GCInterval%time.Second == 0 {
		t.Fatal("fixture did not exercise positive fractional overflow")
	}
	certified, err := NewManagedCapacityRuntime(t.Context(), managedCapacityTestManager(t, config), owner)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.Effective != nil || actual.EffectiveGeneration != 0 || actual.Error != securitytransport.OperationalErrorInvalidPolicy ||
		actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_CAPACITY_GC_INTERVAL_SECONDS] != securitytransport.OperationalSourceEnvironment {
		t.Fatalf("invalid bootstrap = %+v, %v", actual, err)
	}
	policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: operationalpolicy.Values{Capacity: &operationalpolicy.Capacity{GCIntervalSeconds: 1}}}
	if err := certified.adapter.ApplyOperationalPolicy(policy); err != nil {
		t.Fatal(err)
	}
	actual, err = certified.adapter.ObserveOperationalPolicy()
	if err != nil || actual.Effective == nil || actual.Effective.Capacity.GCIntervalSeconds != 1 || actual.Error != securitytransport.OperationalErrorNone || actual.EffectiveGeneration != 1 {
		t.Fatalf("managed repair = %+v, %v", actual, err)
	}
}
