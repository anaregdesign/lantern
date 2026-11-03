package provider

import (
	"context"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/service"
)

type managedLifecycleCertified struct {
	manager *ManagedOperationalRuntime
	owner   *service.LifecyclePolicyOwner
	plan    *service.ShutdownPlan
	gate    *readiness.Gate
	adapter *managedLifecycleRuntime
}

// This lock orders actual future-action policy publication and readiness
// reevaluation. Callbacks never read authority or the management runtime.
type managedLifecycleRuntime struct {
	mu               sync.Mutex
	owner            *service.LifecyclePolicyOwner
	plan             *service.ShutdownPlan
	gate             *readiness.Gate
	bootstrapSources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
}

func NewManagedLifecycleRuntime(ctx context.Context, manager *ManagedOperationalRuntime, shutdown ShutdownConfig, readinessConfig ReadinessConfig, gate *readiness.Gate) (managedLifecycleCertified, error) {
	if manager == nil || gate == nil || ctx.Err() != nil {
		return managedLifecycleCertified{}, errManagedOperational
	}
	sources, err := manager.BootstrapSources(operationalpolicy.GroupLifecycle)
	if err != nil {
		return managedLifecycleCertified{}, err
	}
	initial := service.LifecyclePolicySnapshot{Policy: service.LifecyclePolicy{
		DrainDelay: shutdown.DrainDelay, ShutdownTimeout: shutdown.Timeout, MaxReplicationLag: readinessConfig.MaxLag,
	}}
	if initial.Policy.DrainDelay < 0 {
		initial.Policy.DrainDelay = 0
		sources[securitytransport.OperationalSetting(25)] = securitytransport.OperationalSourceDerived
	}
	if initial.Policy.ShutdownTimeout < 0 {
		initial.Policy.ShutdownTimeout = 0
		sources[securitytransport.OperationalSetting(27)] = securitytransport.OperationalSourceDerived
	}
	// Construct from verified desired state when already adopted. Invalid or
	// conflicting host input must not block or replace authoritative recovery.
	var desired *operationalpolicy.Policy
	if manager.authority != nil {
		projection, readErr := manager.authority.CurrentOperationalPolicies(ctx)
		if readErr != nil {
			return managedLifecycleCertified{}, readErr
		}
		manager.mu.Lock()
		readErr = manager.acceptProjection(projection)
		manager.mu.Unlock()
		if readErr != nil {
			return managedLifecycleCertified{}, readErr
		}
		desired = projection.Policies.Get(operationalpolicy.GroupLifecycle)
	}
	var owner *service.LifecyclePolicyOwner
	if desired != nil {
		initial, err = managedLifecycleSnapshot(*desired)
		if err != nil {
			return managedLifecycleCertified{}, err
		}
		owner, err = service.NewLifecyclePolicyOwner(initial)
		if err != nil {
			return managedLifecycleCertified{}, err
		}
	} else {
		owner = service.NewBootstrapLifecyclePolicyOwner(initial.Policy)
		if gate.MaxLag() != initial.Policy.MaxReplicationLag {
			return managedLifecycleCertified{}, errManagedOperational
		}
	}
	plan, err := service.NewShutdownPlan(owner)
	if err != nil {
		return managedLifecycleCertified{}, err
	}
	adapter := &managedLifecycleRuntime{owner: owner, plan: plan, gate: gate, bootstrapSources: sources}
	if err := manager.Register(ctx, operationalpolicy.GroupLifecycle, adapter); err != nil {
		return managedLifecycleCertified{}, err
	}
	return managedLifecycleCertified{manager, owner, plan, gate, adapter}, nil
}

func managedLifecycleSnapshot(policy operationalpolicy.Policy) (service.LifecyclePolicySnapshot, error) {
	if policy.Validate() != nil || policy.Values.Lifecycle == nil {
		return service.LifecyclePolicySnapshot{}, errManagedOperational
	}
	values := policy.Values.Lifecycle
	// Domain validation checks representability before either multiplication.
	return service.LifecyclePolicySnapshot{Generation: policy.Generation, Policy: service.LifecyclePolicy{
		DrainDelay:        time.Duration(values.DrainDelaySeconds) * time.Second,
		ShutdownTimeout:   time.Duration(values.ShutdownTimeoutSeconds) * time.Second,
		MaxReplicationLag: values.MaxReplicationLag,
	}}, nil
}

func (a *managedLifecycleRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if a == nil || a.owner == nil || a.plan == nil || a.gate == nil || group != operationalpolicy.GroupLifecycle || version != operationalpolicy.Version {
		return errManagedOperational
	}
	return nil
}

func (a *managedLifecycleRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if a.AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, operationalpolicy.Version) != nil || change.Validate() != nil || change.Values.Lifecycle == nil {
		return errManagedOperational
	}
	return nil
}

func (a *managedLifecycleRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if a.AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, operationalpolicy.Version) != nil {
		return errManagedOperational
	}
	next, err := managedLifecycleSnapshot(policy)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.owner.Apply(next); err != nil {
		return err
	}
	// Update only this threshold. Native permission, bootstrap, Search and
	// the one-way drain latch remain independent readiness conditions.
	a.gate.UpdateMaxLag(next.Policy.MaxReplicationLag)
	return nil
}

func (a *managedLifecycleRuntime) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if a.AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, operationalpolicy.Version) != nil {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	actual := a.owner.LifecycleSnapshot()
	if a.gate.MaxLag() != actual.Policy.MaxReplicationLag {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	observation := OperationalOwnerObservation{EffectiveGeneration: actual.Generation, Mode: securitytransport.OperationalModeHot,
		Error: securitytransport.OperationalErrorNone, Sources: cloneOperationalSources(a.bootstrapSources)}
	if actual.Generation != 0 {
		observation.Sources = map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource{
			securitytransport.OperationalSetting(25): securitytransport.OperationalSourceManaged,
			securitytransport.OperationalSetting(26): securitytransport.OperationalSourceManaged,
			securitytransport.OperationalSetting(27): securitytransport.OperationalSourceManaged,
		}
	}
	if actual.Policy.Validate() != nil {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	values := operationalpolicy.Values{Lifecycle: &operationalpolicy.Lifecycle{DrainDelaySeconds: int64(actual.Policy.DrainDelay / time.Second),
		ShutdownTimeoutSeconds: int64(actual.Policy.ShutdownTimeout / time.Second), MaxReplicationLag: actual.Policy.MaxReplicationLag}}
	observation.Effective = &values
	return observation, nil
}

// NewManagedShutdownPlan selects the concrete shared plan without depending on
// the HTTP server. It can therefore participate in pre-listener certification.
func NewManagedShutdownPlan(lifecycle managedLifecycleCertified) (*service.ShutdownPlan, error) {
	if lifecycle.manager == nil || lifecycle.adapter == nil || lifecycle.adapter.owner != lifecycle.owner || lifecycle.adapter.plan != lifecycle.plan || lifecycle.adapter.gate != lifecycle.gate ||
		lifecycle.adapter.AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, operationalpolicy.Version) != nil {
		return nil, errManagedOperational
	}
	lifecycle.manager.mu.Lock()
	entry := lifecycle.manager.lifecycle
	lifecycle.manager.mu.Unlock()
	if entry == nil || entry.owner != lifecycle.adapter {
		return nil, errManagedOperational
	}
	return lifecycle.plan, nil
}

// NewManagedLifecycleConfig binds the same pre-listener plan into HTTP server
// construction. App receives NewManagedShutdownPlan's exact pointer separately.
func NewManagedLifecycleConfig(cache CacheConfig, lifecycle managedLifecycleCertified) (service.LifecycleConfig, error) {
	plan, err := NewManagedShutdownPlan(lifecycle)
	if err != nil {
		return service.LifecycleConfig{}, err
	}
	return service.LifecycleConfig{GCInterval: cache.GCInterval, ShutdownTimeout: lifecycle.owner.LifecycleSnapshot().Policy.ShutdownTimeout, ShutdownPlan: plan}, nil
}
