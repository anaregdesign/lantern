package provider

import (
	"context"
	"math"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

func managedRequestBootstrap() OperationalBootstrapConfig {
	bootstrap := OperationalBootstrapConfig{captured: true}
	for index := range bootstrap.sources {
		bootstrap.sources[index] = OperationalBootstrapDefault
	}
	return bootstrap
}

func managedRequestFixture(t *testing.T, validation ValidationLimits) (*ManagedRequestRuntime, *ManagedOperationalRuntime, *dataruntime.Executor, *ValidationInterceptor) {
	t.Helper()
	data := securityDataFixture(t)
	executor := data.NewExecutor(nil).WithScanLimits(dataruntime.ScanLimits{ScanDefaultLimit: 3, ScanMaxLimit: 4, DeleteByPrefixDefaultLimit: 5, DeleteByPrefixMaxLimit: 6}).WithTraversalTimeout(17 * time.Millisecond).WithTraversalLimits(dataruntime.TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxPushes: 91, MaxTouchedEdges: 92}, MaxResults: 93})
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off", data: data}, managedRequestBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	validator := NewValidationInterceptor(validation)
	runtime, err := NewManagedRequestRuntime(t.Context(), manager, executor, validator)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, manager, executor, validator
}

func TestManagedRequestRuntimeUsesActualSharedOwnerAndDerivedDisabledBounds(t *testing.T) {
	runtime, _, executor, validator := managedRequestFixture(t, ValidationLimits{MaxKeyLen: -1, MaxBatchSize: -2, IlluminateMaxStep: -3, IlluminateMaxK: -4})
	actual, err := runtime.ObserveOperationalPolicy()
	if err != nil || actual.Effective == nil {
		t.Fatalf("observation = %+v/%v", actual, err)
	}
	v := actual.Effective.Request
	if v.MaxKeyLen != 0 || v.MaxBatchSize != 0 || v.IlluminateMaxK != 0 || v.IlluminateMaxStep != 0 || v.ScanDefaultLimit != 3 || v.ScanMaxLimit != 4 || v.DeleteByPrefixDefaultLimit != 5 || v.DeleteByPrefixMaxLimit != 6 || v.TraversalTimeoutMS != 17 || v.TraversalMaxPushes != 91 || v.TraversalMaxTouchedEdges != 92 || v.TraversalMaxResults != 93 {
		t.Fatalf("actual inputs replaced: %+v", v)
	}
	for _, key := range []securitytransport.OperationalSetting{6, 7, 8, 9} {
		if actual.Sources[key] != securitytransport.OperationalSourceDerived {
			t.Fatalf("normalized source %d = %v", key, actual.Sources[key])
		}
	}
	if actual.EffectiveGeneration != 0 || actual.Fingerprint != "" || actual.Mode != securitytransport.OperationalModeHot {
		t.Fatalf("bootstrap invented management: %+v", actual)
	}
	if !executor.UsesRequestPolicyOwner(runtime.owner) || validator.policies != runtime.owner {
		t.Fatal("different request owners installed")
	}
	actual.Sources[6] = securitytransport.OperationalSourceManaged
	fresh, _ := runtime.ObserveOperationalPolicy()
	if fresh.Sources[6] != securitytransport.OperationalSourceDerived {
		t.Fatal("source map aliased")
	}
	validator.WithRequestPolicySource(dataruntime.NewBootstrapRequestPolicyOwner(runtime.owner.RequestPolicySnapshot().Policy))
	if runtime.AvailableOperationalPolicy(operationalpolicy.GroupRequest, 1) == nil {
		t.Fatal("substituted validator owner certified")
	}
}

func TestManagedRequestRuntimeOneGenerationAcrossValidationAndExecution(t *testing.T) {
	runtime, _, _, validator := managedRequestFixture(t, ValidationLimits{MaxKeyLen: 100, MaxBatchSize: 100})
	before := runtime.owner.RequestPolicySnapshot()
	values, err := operationalpolicy.Defaults(operationalpolicy.GroupRequest)
	if err != nil {
		t.Fatal(err)
	}
	values.Request.MaxKeyLen = 1
	policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}
	called := 0
	wrapped := validator.ConnectInterceptor()(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		called++
		captured, ok := dataruntime.RequestPolicyFromContext(ctx)
		if !ok || captured != before {
			t.Errorf("validator did not bind initial generation: %+v/%v", captured, ok)
		}
		if err := runtime.ApplyOperationalPolicy(policy); err != nil {
			return nil, err
		}
		if after, _ := dataruntime.RequestPolicyFromContext(ctx); after != before {
			t.Error("captured request changed during Apply")
		}
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	if _, err := wrapped(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: "long"})); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: "long"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("new validation did not apply: %v", err)
	}
	if called != 1 {
		t.Fatalf("handler called %d times", called)
	}
	actual, err := runtime.ObserveOperationalPolicy()
	if err != nil || actual.EffectiveGeneration != 1 || actual.Effective.Request.MaxKeyLen != 1 {
		t.Fatalf("managed effective = %+v/%v", actual, err)
	}
	for key, source := range actual.Sources {
		if source != securitytransport.OperationalSourceManaged {
			t.Fatalf("managed source %d = %v", key, source)
		}
	}
}

func TestManagedRequestRuntimeInvalidBootstrapCanBeExplicitlyRepaired(t *testing.T) {
	data := securityDataFixture(t)
	executor := data.NewExecutor(nil).WithScanLimits(dataruntime.ScanLimits{})
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off", data: data}, managedRequestBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewManagedRequestRuntime(t.Context(), manager, executor, NewValidationInterceptor(ValidationLimits{}))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := runtime.ObserveOperationalPolicy()
	if err != nil || actual.Effective != nil || actual.Error != securitytransport.OperationalErrorInvalidPolicy || len(actual.Sources) != 12 || actual.EffectiveGeneration != 0 {
		t.Fatalf("invalid bootstrap fabricated values: %+v/%v", actual, err)
	}
	values, _ := operationalpolicy.Defaults(operationalpolicy.GroupRequest)
	change := operationalpolicy.Change{Values: values}
	if err := runtime.PrepareOperationalPolicy(change); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyOperationalPolicy(operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}); err != nil {
		t.Fatal(err)
	}
	actual, err = runtime.ObserveOperationalPolicy()
	if err != nil || actual.Effective == nil || actual.EffectiveGeneration != 1 || actual.Error != securitytransport.OperationalErrorNone {
		t.Fatalf("explicit repair failed: %+v/%v", actual, err)
	}
}

func TestManagedRequestRuntimeCheckedConversionsAndBindingRefusals(t *testing.T) {
	runtime, manager, executor, validator := managedRequestFixture(t, ValidationLimits{})
	if _, err := NewManagedRequestRuntime(t.Context(), manager, executor, validator); err == nil {
		t.Fatal("duplicate owner binding accepted")
	}
	foreign := dataruntime.NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute))
	if _, err := NewManagedRequestRuntime(t.Context(), manager, foreign, NewValidationInterceptor(ValidationLimits{})); err == nil {
		t.Fatal("foreign executor accepted")
	}
	if runtime.AvailableOperationalPolicy(operationalpolicy.GroupSearch, 1) == nil || runtime.AvailableOperationalPolicy(operationalpolicy.GroupRequest, 2) == nil {
		t.Fatal("wrong group/version supported")
	}
	for _, mutate := range []func(*operationalpolicy.Request){
		func(v *operationalpolicy.Request) { v.ScanMaxLimit = math.MaxUint32 + 1 },
		func(v *operationalpolicy.Request) { v.TraversalTimeoutMS = math.MaxInt64/int64(time.Millisecond) + 1 },
		func(v *operationalpolicy.Request) { v.MaxKeyLen = -1 },
	} {
		values, _ := operationalpolicy.Defaults(operationalpolicy.GroupRequest)
		mutate(values.Request)
		before := runtime.owner.RequestPolicySnapshot()
		if runtime.PrepareOperationalPolicy(operationalpolicy.Change{Values: values}) == nil || runtime.ApplyOperationalPolicy(operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}) == nil {
			t.Fatal("invalid authoritative candidate accepted")
		}
		if runtime.owner.RequestPolicySnapshot() != before {
			t.Fatal("rejected update changed effective owner")
		}
	}
}

func TestManagedRequestRuntimeRestoresVerifiedPolicyBeforeBinding(t *testing.T) {
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(3), operationalpolicy.GroupRequest)
	values, _ := operationalpolicy.Defaults(operationalpolicy.GroupRequest)
	values.Request.MaxKeyLen = 1
	policy := &operationalpolicy.Policy{Version: 1, Generation: 7, Values: values}
	if err := p.Policies.Set(operationalpolicy.GroupRequest, policy); err != nil {
		t.Fatal(err)
	}
	manager, authority := operationalRuntimeTestManager(t, p)
	manager.bootstrap = managedRequestBootstrap()
	data := securityDataFixture(t)
	manager.runtime.data = data
	executor := data.NewExecutor(nil)
	validator := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 999})
	authority.onBind = func() {
		if validator.policies == nil {
			t.Error("native binding preceded actual owner installation")
			return
		}
		snapshot := validator.policies.RequestPolicySnapshot()
		if snapshot.Generation != 7 || snapshot.Policy.Validation.MaxKeyLen != 1 {
			t.Errorf("binding saw environment instead of verified policy: %+v", snapshot)
		}
	}
	runtime, err := NewManagedRequestRuntime(t.Context(), manager, executor, validator)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := manager.Observe(p)
	if err != nil || observed[operationalpolicy.GroupRequest].AppliedCut != p.Cut || observed[operationalpolicy.GroupRequest].AppliedGeneration != 7 {
		t.Fatalf("verified installation provenance = %+v/%v", observed, err)
	}
	reads := authority.reads
	policy.Generation++
	if err := runtime.PrepareOperationalPolicy(operationalpolicy.Change{ExpectedGeneration: 7, Values: values}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyOperationalPolicy(*policy); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ObserveOperationalPolicy(); err != nil {
		t.Fatal(err)
	}
	if authority.reads != reads {
		t.Fatal("native callback re-entered authority")
	}
}
