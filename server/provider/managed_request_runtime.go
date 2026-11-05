package provider

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

// ManagedRequestRuntime binds transport validation and every deep request
// consumer to one actual atomic owner. Authority, persistence and applied-cut
// provenance remain with ManagedOperationalRuntime.
type ManagedRequestRuntime struct {
	manager          *ManagedOperationalRuntime
	owner            *dataruntime.RequestPolicyOwner
	executor         *dataruntime.Executor
	validator        *ValidationInterceptor
	bootstrapSources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
}

func NewManagedRequestRuntime(ctx context.Context, manager *ManagedOperationalRuntime, executor *dataruntime.Executor, validator *ValidationInterceptor) (*ManagedRequestRuntime, error) {
	if manager == nil || manager.runtime == nil || executor == nil || !executor.SharesServingRuntime(manager.runtime.data) || validator == nil || validator.policies != nil || ctx.Err() != nil {
		return nil, security.ErrOperationalPolicyUnavailable
	}
	sources, err := manager.BootstrapSources(operationalpolicy.GroupRequest)
	if err != nil || len(sources) != 12 {
		return nil, security.ErrOperationalPolicyUnavailable
	}
	actual, err := executor.BootstrapRequestPolicy(dataruntime.RequestValidationLimits(validator.limits))
	if err != nil {
		return nil, err
	}
	// Legacy nonpositive validation bounds disable a dimension. Canonical zero
	// preserves that exact behavior; a changed representation is derived.
	for _, bound := range []struct {
		value   *int
		setting securitytransport.OperationalSetting
	}{
		{&actual.Validation.IlluminateMaxK, 6}, {&actual.Validation.IlluminateMaxStep, 7},
		{&actual.Validation.MaxBatchSize, 8}, {&actual.Validation.MaxKeyLen, 9},
	} {
		if *bound.value < 0 {
			*bound.value = 0
			sources[bound.setting] = securitytransport.OperationalSourceDerived
		}
	}
	owner := dataruntime.NewBootstrapRequestPolicyOwner(actual)
	runtime := &ManagedRequestRuntime{manager: manager, owner: owner, executor: executor, validator: validator, bootstrapSources: sources}
	executor.WithRequestPolicySource(owner)
	validator.WithRequestPolicySource(owner)
	if err := manager.Register(ctx, operationalpolicy.GroupRequest, runtime); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *ManagedRequestRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if r == nil || group != operationalpolicy.GroupRequest || version != operationalpolicy.Version || r.manager == nil || r.manager.runtime == nil || r.owner == nil || r.validator == nil || r.validator.policies != r.owner || !r.executor.UsesRequestPolicyOwner(r.owner) || !r.executor.SharesServingRuntime(r.manager.runtime.data) {
		return security.ErrOperationalPolicyUnavailable
	}
	return nil
}

func requestRuntimePolicy(values operationalpolicy.Values) (dataruntime.RequestPolicy, error) {
	group, err := values.Group()
	if err != nil || group != operationalpolicy.GroupRequest || values.Validate() != nil {
		return dataruntime.RequestPolicy{}, operationalpolicy.ErrInvalid
	}
	v := values.Request
	for _, value := range []int64{v.IlluminateMaxK, v.IlluminateMaxStep, v.MaxBatchSize, v.MaxKeyLen, v.TraversalMaxPushes, v.TraversalMaxResults, v.TraversalMaxTouchedEdges} {
		if int64(int(value)) != value {
			return dataruntime.RequestPolicy{}, operationalpolicy.ErrInvalid
		}
	}
	policy := dataruntime.RequestPolicy{
		Validation:       dataruntime.RequestValidationLimits{MaxKeyLen: int(v.MaxKeyLen), MaxBatchSize: int(v.MaxBatchSize), IlluminateMaxStep: int(v.IlluminateMaxStep), IlluminateMaxK: int(v.IlluminateMaxK)},
		Scan:             dataruntime.ScanLimits{ScanDefaultLimit: uint32(v.ScanDefaultLimit), ScanMaxLimit: uint32(v.ScanMaxLimit), DeleteByPrefixDefaultLimit: uint32(v.DeleteByPrefixDefaultLimit), DeleteByPrefixMaxLimit: uint32(v.DeleteByPrefixMaxLimit)},
		Traversal:        dataruntime.TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxPushes: int(v.TraversalMaxPushes), MaxTouchedEdges: int(v.TraversalMaxTouchedEdges)}, MaxResults: int(v.TraversalMaxResults)},
		TraversalTimeout: time.Duration(v.TraversalTimeoutMS) * time.Millisecond,
	}
	return policy, policy.Validate()
}

func (r *ManagedRequestRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupRequest, operationalpolicy.Version) != nil || change.Validate() != nil {
		return security.ErrOperationalPolicyUnavailable
	}
	_, err := requestRuntimePolicy(change.Values)
	return err
}

func (r *ManagedRequestRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupRequest, operationalpolicy.Version) != nil || policy.Validate() != nil {
		return security.ErrOperationalPolicyUnavailable
	}
	actual, err := requestRuntimePolicy(policy.Values)
	if err != nil {
		return err
	}
	return r.owner.Apply(dataruntime.RequestPolicySnapshot{Generation: policy.Generation, Policy: actual})
}

func (r *ManagedRequestRuntime) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupRequest, operationalpolicy.Version) != nil {
		return OperationalOwnerObservation{}, security.ErrOperationalPolicyUnavailable
	}
	snapshot := r.owner.RequestPolicySnapshot()
	observation := OperationalOwnerObservation{EffectiveGeneration: snapshot.Generation, Sources: cloneOperationalSources(r.bootstrapSources), Mode: securitytransport.OperationalModeHot, Error: securitytransport.OperationalErrorNone}
	if snapshot.Policy.Validate() != nil {
		if snapshot.Generation != 0 {
			return OperationalOwnerObservation{}, security.ErrOperationalPolicyUnavailable
		}
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	p := snapshot.Policy
	values := operationalpolicy.Values{Request: &operationalpolicy.Request{
		DeleteByPrefixDefaultLimit: int64(p.Scan.DeleteByPrefixDefaultLimit), DeleteByPrefixMaxLimit: int64(p.Scan.DeleteByPrefixMaxLimit),
		IlluminateMaxK: int64(p.Validation.IlluminateMaxK), IlluminateMaxStep: int64(p.Validation.IlluminateMaxStep), MaxBatchSize: int64(p.Validation.MaxBatchSize), MaxKeyLen: int64(p.Validation.MaxKeyLen),
		ScanDefaultLimit: int64(p.Scan.ScanDefaultLimit), ScanMaxLimit: int64(p.Scan.ScanMaxLimit), TraversalMaxPushes: int64(p.Traversal.WorkBudget.MaxPushes), TraversalMaxResults: int64(p.Traversal.MaxResults),
		TraversalMaxTouchedEdges: int64(p.Traversal.WorkBudget.MaxTouchedEdges), TraversalTimeoutMS: p.TraversalTimeout.Milliseconds(),
	}}
	if values.Validate() != nil {
		return OperationalOwnerObservation{}, security.ErrOperationalPolicyUnavailable
	}
	observation.Effective = &values
	if snapshot.Generation != 0 {
		for key := range observation.Sources {
			observation.Sources[key] = securitytransport.OperationalSourceManaged
		}
	}
	return observation, nil
}
