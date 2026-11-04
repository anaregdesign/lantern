package provider

import (
	"context"
	"math"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

type managedRateCertified struct {
	manager  *ManagedOperationalRuntime
	owner    *RateLimitInterceptor
	recovery *rateRecoveryRuntime
	adapter  *managedRateRuntime
}

type managedRateRuntime struct {
	manager          *ManagedOperationalRuntime
	owner            *RateLimitInterceptor
	recovery         *rateRecoveryRuntime
	bootstrapSources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
}

func NewManagedRateRuntime(ctx context.Context, manager *ManagedOperationalRuntime, owner *RateLimitInterceptor, recovery *rateRecoveryRuntime) (managedRateCertified, error) {
	if manager == nil || ctx.Err() != nil {
		return managedRateCertified{}, errManagedOperational
	}
	sources, err := manager.BootstrapSources(operationalpolicy.GroupRate)
	if err != nil {
		return managedRateCertified{}, err
	}
	adapter := &managedRateRuntime{manager: manager, owner: owner, recovery: recovery, bootstrapSources: sources}
	if adapter.AvailableOperationalPolicy(operationalpolicy.GroupRate, operationalpolicy.Version) != nil {
		return managedRateCertified{}, errManagedOperational
	}
	if err := manager.Register(ctx, operationalpolicy.GroupRate, adapter); err != nil {
		return managedRateCertified{}, err
	}
	return managedRateCertified{manager, owner, recovery, adapter}, nil
}

func (r *managedRateRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if r == nil || r.manager == nil || r.owner == nil || group != operationalpolicy.GroupRate || version != operationalpolicy.Version || !r.recovery.bound(r.manager.runtime, r.owner) {
		return errManagedOperational
	}
	if r.manager.runtime.mode == "oidc" && !r.recovery.available(r.manager.runtime, r.owner) {
		return errManagedOperational
	}
	r.owner.mu.Lock()
	clockInstalled := r.owner.now != nil
	r.owner.mu.Unlock()
	if !clockInstalled {
		return errManagedOperational
	}
	return nil
}

func managedRateConfig(values operationalpolicy.Values) (RateLimitConfig, error) {
	if values.Validate() != nil || values.Rate == nil || values.Rate.Burst > int64(^uint(0)>>1) {
		return RateLimitConfig{}, errManagedOperational
	}
	return RateLimitConfig{RPS: values.Rate.RPS, Burst: int(values.Rate.Burst)}, nil
}

func (r *managedRateRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupRate, operationalpolicy.Version) != nil || change.Validate() != nil {
		return errManagedOperational
	}
	_, err := managedRateConfig(change.Values)
	return err
}

func (r *managedRateRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupRate, operationalpolicy.Version) != nil || policy.Validate() != nil {
		return errManagedOperational
	}
	config, err := managedRateConfig(policy.Values)
	if err != nil {
		return err
	}
	return r.owner.applyManagedRate(config, policy.Generation)
}

func (r *managedRateRuntime) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupRate, operationalpolicy.Version) != nil {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	actual := r.owner.Snapshot()
	observation := OperationalOwnerObservation{
		EffectiveGeneration: actual.Generation, Sources: r.sourcesFor(actual),
		Mode: securitytransport.OperationalModeHot, Error: securitytransport.OperationalErrorNone,
	}
	if actual.Error != nil {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	values := operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: actual.Config.RPS, Burst: int64(actual.Config.Burst)}}
	if values.Validate() != nil {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	observation.Effective = &values
	return observation, nil
}

func (r *managedRateRuntime) sourcesFor(actual RateLimitSnapshot) map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource {
	if actual.Generation != 0 {
		return map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource{
			pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS:   securitytransport.OperationalSourceManaged,
			pb.OperationalSetting_OPERATIONAL_SETTING_RATE_BURST: securitytransport.OperationalSourceManaged,
		}
	}
	sources := cloneOperationalSources(r.bootstrapSources)
	if actual.Error != nil || sources == nil {
		return sources
	}
	// Compare each observation against immutable typed constructor input and
	// same-load provenance. A preadoption Configure can change actual values;
	// it must not retain stale default/environment labels or mutate the baseline.
	if actual.Config.RPS != r.owner.bootstrap.RPS || math.Signbit(actual.Config.RPS) != math.Signbit(r.owner.bootstrap.RPS) {
		sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS] = securitytransport.OperationalSourceDerived
	}
	if actual.Config.Burst != r.owner.bootstrap.Burst {
		sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_BURST] = securitytransport.OperationalSourceDerived
	}
	return sources
}
