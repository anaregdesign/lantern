package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

func managedSearchBootstrap() OperationalBootstrapConfig {
	bootstrap := OperationalBootstrapConfig{captured: true}
	for index := range bootstrap.sources {
		bootstrap.sources[index] = OperationalBootstrapDefault
	}
	return bootstrap
}

func managedSearchTestPrimary(t *testing.T) (*dataruntime.ServingRuntime, *dataruntime.Executor) {
	t.Helper()
	data := securityDataFixture(t)
	ConfigureGraphCache(data.GraphCache(), CacheConfig{namespaceFormat: "namespaced-v1"}, SearchConfig{Enabled: true, Positions: true})
	limits := query.DefaultLimits()
	limits.Enabled, limits.PositionsEnabled, limits.DefaultMinShould = true, true, 1
	return data, data.NewExecutor(nil).WithSearchLimits(limits)
}

func managedSearchTestPolicy(t *testing.T, primary *dataruntime.Executor, generation uint64) *operationalpolicy.Policy {
	t.Helper()
	values, err := operationalpolicy.Defaults(operationalpolicy.GroupSearch)
	if err != nil {
		t.Fatal(err)
	}
	values.Search.MaxQueryBytes--
	policy, err := searchRuntimePolicy(values)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := dataruntime.ResolveSearchPolicy(primary.SearchPolicyBaseline(), generation, policy)
	if err != nil {
		t.Fatal(err)
	}
	return &operationalpolicy.Policy{Version: 1, Generation: generation, Values: values, CompatibilityFingerprint: resolved.Version.Fingerprint}
}

func TestManagedSearchRuntimeSealsExactInstallationAndStagesLiveChanges(t *testing.T) {
	data, primary := managedSearchTestPrimary(t)
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off", data: data}, managedSearchBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	beforeFP := primary.SearchConfigFingerprint()
	certified, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	if !certified.validFor(manager, primary) || (managedSearchCertified{}).validFor(manager, primary) {
		t.Fatal("search certification did not bind actual nonzero installation")
	}
	runtime := certified.runtime
	before := runtime.owner.Status()
	policy := managedSearchTestPolicy(t, primary, 1)
	preview, err := runtime.PreviewOperationalPolicy(policy.Values)
	if err != nil || preview != policy.CompatibilityFingerprint || runtime.owner.Status() != before {
		t.Fatalf("read-only candidate preview = %s/%v", preview, err)
	}
	change := operationalpolicy.Change{Values: policy.Values, CompatibilityFingerprint: preview}
	if err := runtime.PrepareOperationalPolicy(change); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyOperationalPolicy(*policy); err != nil {
		t.Fatal(err)
	}
	actual, err := runtime.ObserveOperationalPolicy()
	if err != nil || actual.EffectiveGeneration != 0 || actual.Fingerprint != beforeFP || actual.Mode != securitytransport.OperationalModeCoordinatedRestart || !runtime.owner.Status().PendingRestart {
		t.Fatalf("live commit falsely installed engine: %+v/%v", actual, err)
	}
	if primary.SearchConfigFingerprint() != beforeFP || !certified.validFor(manager, primary) {
		t.Fatal("live callback drifted certified peer fingerprint/engine")
	}
	if _, err := primary.Public().SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "needle"}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("pending direct Search admitted: %v", err)
	}
	if _, err := primary.Public().GetServerStatus(t.Context(), &pb.GetServerStatusRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManagedSearchRuntime(t.Context(), manager, primary); err == nil {
		t.Fatal("duplicate search installation accepted")
	}
	foreign := data.NewExecutor(nil).WithSearchLimits(primary.SearchPolicyBaseline())
	if certified.validFor(manager, foreign) {
		t.Fatal("foreign primary accepted by certificate")
	}
	// The public marker cannot be reused after an out-of-contract manual engine
	// replacement, even if another engine happens to have valid typed policy.
	if err := runtime.owner.Restart(runtime.owner.Status().Desired); err != nil {
		t.Fatal(err)
	}
	if certified.validFor(manager, primary) || runtime.AvailableOperationalPolicy(operationalpolicy.GroupSearch, 1) == nil {
		t.Fatal("substituted engine retained certification")
	}
}

func TestManagedSearchRuntimeRestoresVerifiedPolicyBeforeNativeBind(t *testing.T) {
	data, primary := managedSearchTestPrimary(t)
	policy := managedSearchTestPolicy(t, primary, 7)
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(3), operationalpolicy.GroupSearch)
	if err := p.Policies.Set(operationalpolicy.GroupSearch, policy); err != nil {
		t.Fatal(err)
	}
	manager, authority := operationalRuntimeTestManager(t, p)
	manager.bootstrap = managedSearchBootstrap()
	manager.runtime.data = data
	manager.runtime.managedOperational = manager
	authority.onBind = func() {
		if primary.SearchConfigFingerprint() != policy.CompatibilityFingerprint {
			t.Error("native binding preceded exact recovered engine installation")
		}
	}
	certified, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	if !certified.validFor(manager, primary) {
		t.Fatal("recovered installation not certified")
	}
	actual, err := certified.runtime.ObserveOperationalPolicy()
	if err != nil || actual.EffectiveGeneration != 7 || actual.Fingerprint != policy.CompatibilityFingerprint || actual.Effective == nil || actual.Effective.Search.MaxQueryBytes != policy.Values.Search.MaxQueryBytes {
		t.Fatalf("verified restored effective = %+v/%v", actual, err)
	}
	for field, source := range actual.Sources {
		if source != securitytransport.OperationalSourceManaged {
			t.Fatalf("managed source %d = %v", field, source)
		}
	}
	observed, err := manager.Observe(p)
	if err != nil || observed[operationalpolicy.GroupSearch].AppliedCut != p.Cut || observed[operationalpolicy.GroupSearch].AppliedGeneration != 7 {
		t.Fatalf("restore provenance = %+v/%v", observed, err)
	}
	reads := authority.reads
	policy.Generation++
	if err := certified.runtime.PrepareOperationalPolicy(operationalpolicy.Change{ExpectedGeneration: 7, Values: policy.Values, CompatibilityFingerprint: policy.CompatibilityFingerprint}); err != nil {
		t.Fatal(err)
	}
	if err := certified.runtime.ApplyOperationalPolicy(*policy); err != nil {
		t.Fatal(err)
	}
	if _, err := certified.runtime.ObserveOperationalPolicy(); err != nil {
		t.Fatal(err)
	}
	if authority.reads != reads {
		t.Fatal("native callback re-entered authority")
	}
	if primary.SearchConfigFingerprint() != policy.CompatibilityFingerprint || certified.runtime.owner.Status().Effective.Version.Generation != 7 {
		t.Fatal("post-certification callback restarted engine")
	}
}

func TestManagedSearchRuntimeUnmanageableBootstrapKeepsActualFingerprint(t *testing.T) {
	data, primary := managedSearchTestPrimary(t)
	limits := primary.SearchPolicyBaseline()
	limits.MaxQueryBytes = int(int64(1) << 32)
	primary.WithSearchLimits(limits)
	actualFP := primary.SearchConfigFingerprint()
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off", data: data}, managedSearchBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := certified.runtime.ObserveOperationalPolicy()
	if err != nil || actual.Effective != nil || actual.Error != securitytransport.OperationalErrorInvalidPolicy || actual.Fingerprint != actualFP || actual.EffectiveGeneration != 0 || len(actual.Sources) != 11 {
		t.Fatalf("invalid bootstrap fabricated state: %+v/%v", actual, err)
	}
	policy := managedSearchTestPolicy(t, primary, 1)
	if err := certified.runtime.ApplyOperationalPolicy(*policy); err != nil {
		t.Fatal(err)
	}
	actual, err = certified.runtime.ObserveOperationalPolicy()
	if err != nil || actual.Effective != nil || actual.Fingerprint != actualFP || !certified.runtime.owner.Status().PendingRestart {
		t.Fatalf("pending repair lost actual state: %+v/%v", actual, err)
	}
	if primary.SearchPolicyBaseline().MaxQueryBytes != limits.MaxQueryBytes || !certified.validFor(manager, primary) {
		t.Fatal("bootstrap values were clamped/replaced")
	}
}

func TestManagedSearchRuntimeRefusesForeignBaselineAndUnavailableAuthority(t *testing.T) {
	data, primary := managedSearchTestPrimary(t)
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off", data: data}, managedSearchBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	values, _ := operationalpolicy.Defaults(operationalpolicy.GroupSearch)
	values.Search.MaxLimit = int64(primary.SearchPolicyBaseline().MaxSessionHits)
	if _, err := certified.runtime.PreviewOperationalPolicy(values); err == nil {
		t.Fatal("excluded session hit cap silently clamped")
	}
	policy := managedSearchTestPolicy(t, primary, 1)
	if err := certified.runtime.PrepareOperationalPolicy(operationalpolicy.Change{Values: policy.Values, CompatibilityFingerprint: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("foreign compatibility fingerprint accepted")
	}
	for _, ctx := range []context.Context{t.Context(), security.WithAdmission(t.Context(), &security.Admission{})} {
		authority := &managedSearchAdmissionAuthority{bootstrap: dataruntime.SearchPolicyVersion{Fingerprint: primary.SearchConfigFingerprint()}}
		if version, err := authority.DesiredSearchPolicy(ctx); err == nil || version != (dataruntime.SearchPolicyVersion{}) {
			t.Fatalf("missing private authority fell back to bootstrap: %+v/%v", version, err)
		}
	}
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(4), operationalpolicy.GroupSearch)
	if err := p.Policies.Set(operationalpolicy.GroupSearch, policy); err != nil {
		t.Fatal(err)
	}
	failing, authority := operationalRuntimeTestManager(t, p)
	failing.bootstrap = managedSearchBootstrap()
	failing.runtime.data = data
	failing.runtime.managedOperational = failing
	authority.readErr = errors.New("authority unavailable")
	other := data.NewExecutor(nil).WithSearchLimits(query.DefaultLimits())
	marker, err := NewManagedSearchRuntime(t.Context(), failing, other)
	if err == nil || marker.validFor(failing, other) || authority.binds != 0 {
		t.Fatal("authority failure produced bootstrap certification")
	}
}

func TestManagedSearchRuntimePreWorkerCatchupIsIncludedInFinalInstallation(t *testing.T) {
	data, primary := managedSearchTestPrimary(t)
	policy := managedSearchTestPolicy(t, primary, 7)
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(3), operationalpolicy.GroupSearch)
	if err := p.Policies.Set(operationalpolicy.GroupSearch, policy); err != nil {
		t.Fatal(err)
	}
	manager, authority := operationalRuntimeTestManager(t, p)
	manager.bootstrap = managedSearchBootstrap()
	manager.runtime.data = data
	manager.runtime.managedOperational = manager
	next := policy.Clone()
	next.Generation++
	next.Values.Search.MaxQueryBytes--
	values, err := searchRuntimePolicy(next.Values)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := dataruntime.ResolveSearchPolicy(primary.SearchPolicyBaseline(), next.Generation, values)
	if err != nil {
		t.Fatal(err)
	}
	next.CompatibilityFingerprint = resolved.Version.Fingerprint
	authority.onBind = func() {
		owner := authority.bound[operationalpolicy.GroupSearch]
		if err := owner.ApplyOperationalPolicy(*next); err != nil {
			t.Error(err)
			return
		}
		advanced := p
		advanced.Cut.Sequence++
		advanced.Cut.Projection[1]++
		if err := advanced.Policies.Set(operationalpolicy.GroupSearch, next); err != nil {
			t.Error(err)
			return
		}
		authority.setProjection(advanced)
	}
	certified, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	if !certified.validFor(manager, primary) || primary.SearchConfigFingerprint() != next.CompatibilityFingerprint || certified.runtime.owner.Status().Effective.Version.Generation != 8 {
		t.Fatal("final worker marker missed pre-worker catchup")
	}
}

func TestManagedSearchRuntimeInvalidBootstrapRepairReportsPendingAndActualFingerprint(t *testing.T) {
	data, primary := managedSearchTestPrimary(t)
	limits := primary.SearchPolicyBaseline()
	limits.MaxQueryBytes = int(int64(1) << 32)
	primary.WithSearchLimits(limits)
	actualFP := primary.SearchConfigFingerprint()
	p := operationalRuntimeTestProjection(1)
	manager, authority := operationalRuntimeTestManager(t, p)
	manager.bootstrap = managedSearchBootstrap()
	manager.runtime.data = data
	manager.runtime.managedOperational = manager
	certified, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	policy := managedSearchTestPolicy(t, primary, 1)
	next := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(2), operationalpolicy.GroupSearch)
	if err := next.Policies.Set(operationalpolicy.GroupSearch, policy); err != nil {
		t.Fatal(err)
	}
	authority.setProjection(next)
	if err := manager.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, err := manager.Observe(next)
	actual := observed[operationalpolicy.GroupSearch]
	if err != nil || actual.Effective != nil || actual.Error != securitytransport.OperationalErrorInvalidPolicy || actual.EffectiveCompatibilityFingerprint != actualFP || actual.AppliedGeneration != 0 || !actual.Pending || !actual.RestartRequired {
		t.Fatalf("pending repair misreported actual installation: %+v/%v", actual, err)
	}
	if !certified.validFor(manager, primary) {
		t.Fatal("staged repair changed peer installation")
	}
}
