package provider

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

type managedSlowRPCCertified struct {
	manager *ManagedOperationalRuntime
	owner   *SlowRPCInterceptor
	adapter *managedSlowRPCRuntime
}

type managedSlowRPCRuntime struct {
	owner            *SlowRPCInterceptor
	bootstrapSources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
}

func NewManagedSlowRPCRuntime(ctx context.Context, manager *ManagedOperationalRuntime, owner *SlowRPCInterceptor, bootstrap ObservabilityConfig) (managedSlowRPCCertified, error) {
	if manager == nil {
		return managedSlowRPCCertified{}, errManagedOperational
	}
	sources, sourceErr := manager.BootstrapSources(operationalpolicy.GroupSlowRPC)
	if sourceErr != nil {
		return managedSlowRPCCertified{}, sourceErr
	}
	adapter := &managedSlowRPCRuntime{owner: owner, bootstrapSources: sources}
	if adapter.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, operationalpolicy.Version) != nil {
		return managedSlowRPCCertified{}, errManagedOperational
	}
	if owner.Threshold() != bootstrap.SlowRPCThreshold && sources != nil {
		sources[securitytransport.OperationalSetting(1)] = securitytransport.OperationalSourceDerived
	}
	if err := manager.Register(ctx, operationalpolicy.GroupSlowRPC, adapter); err != nil {
		return managedSlowRPCCertified{}, err
	}
	return managedSlowRPCCertified{manager, owner, adapter}, nil
}

func (s *managedSlowRPCRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if s == nil || s.owner == nil || s.owner.logger == nil || s.owner.now == nil || group != operationalpolicy.GroupSlowRPC || version != operationalpolicy.Version {
		return errManagedOperational
	}
	return nil
}
func (s *managedSlowRPCRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if s.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, operationalpolicy.Version) != nil || change.Validate() != nil || change.Values.SlowRPC == nil {
		return errManagedOperational
	}
	return nil
}
func (s *managedSlowRPCRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if s.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, operationalpolicy.Version) != nil || policy.Validate() != nil || policy.Values.SlowRPC == nil {
		return errManagedOperational
	}
	return s.owner.ApplyManagedThresholdMilliseconds(policy.Values.SlowRPC.ThresholdMS, policy.Generation)
}
func (s *managedSlowRPCRuntime) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if s.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, operationalpolicy.Version) != nil {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	actual := s.owner.Snapshot()
	observation := OperationalOwnerObservation{EffectiveGeneration: actual.Generation, Mode: securitytransport.OperationalModeHot, Error: securitytransport.OperationalErrorNone, Sources: cloneOperationalSources(s.bootstrapSources)}
	if actual.Generation != 0 {
		observation.Sources = map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource{securitytransport.OperationalSetting(1): securitytransport.OperationalSourceManaged}
	}
	if actual.Threshold < 0 || actual.Threshold%time.Millisecond != 0 {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	values := operationalpolicy.Values{SlowRPC: &operationalpolicy.SlowRPC{ThresholdMS: int64(actual.Threshold / time.Millisecond)}}
	if values.Validate() != nil {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	observation.Effective = &values
	return observation, nil
}
