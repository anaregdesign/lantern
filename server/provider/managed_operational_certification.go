package provider

import (
	"context"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/service"
)

// A concrete group marker forces the real owner to register before public
// certification. Future group packages add their own marker dependencies here;
// absent groups never obtain a dummy application capability.
type managedOperationalCertified struct {
	runtime   *SecurityRuntime
	manager   *ManagedOperationalRuntime
	slowRPC   *SlowRPCInterceptor
	rate      *RateLimitInterceptor
	recovery  *rateRecoveryRuntime
	primary   *dataruntime.Executor
	request   *ManagedRequestRuntime
	validator *ValidationInterceptor
	backup    *backup.Backupper
	capacity  *dataruntime.GraphPolicyOwner
	plan      *service.ShutdownPlan
	search    managedSearchCertified
}

func NewManagedOperationalCertified(ctx context.Context, runtime *SecurityRuntime, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, slow managedSlowRPCCertified, rate managedRateCertified, request *ManagedRequestRuntime, backup managedBackupCertified, capacity managedCapacityCertified, lifecycle managedLifecycleCertified, search managedSearchCertified) (managedOperationalCertified, error) {
	if runtime == nil || manager == nil || manager.runtime != runtime || runtime.managedOperational != manager || slow.manager != manager || slow.owner == nil || slow.adapter == nil || slow.adapter.owner != slow.owner {
		return managedOperationalCertified{}, errManagedOperational
	}
	if rate.manager != manager || rate.owner == nil || rate.adapter == nil || rate.adapter.manager != manager || rate.adapter.owner != rate.owner || rate.adapter.recovery != rate.recovery || !rate.recovery.bound(runtime, rate.owner) {
		return managedOperationalCertified{}, errManagedOperational
	}
	if primary == nil || !primary.SharesServingRuntime(runtime.data) || request == nil || request.manager != manager || request.executor != primary || request.owner == nil || request.validator == nil || request.validator.policies != request.owner || !primary.UsesRequestPolicyOwner(request.owner) {
		return managedOperationalCertified{}, errManagedOperational
	}
	if backup.manager != manager || backup.owner == nil || backup.adapter == nil || backup.adapter.owner != backup.owner || !backup.owner.UsesExecutor(primary) {
		return managedOperationalCertified{}, errManagedOperational
	}
	if capacity.manager != manager || capacity.owner == nil || capacity.adapter == nil || capacity.adapter.owner != capacity.owner || !capacity.owner.OwnsInstallation(runtime.data, primary) {
		return managedOperationalCertified{}, errManagedOperational
	}
	if lifecycle.manager != manager || lifecycle.owner == nil || lifecycle.adapter == nil || lifecycle.adapter.owner != lifecycle.owner || lifecycle.adapter.plan != lifecycle.plan || lifecycle.adapter.gate != lifecycle.gate || !lifecycle.plan.UsesOwner(lifecycle.owner) {
		return managedOperationalCertified{}, errManagedOperational
	}
	if !search.validFor(manager, primary) {
		return managedOperationalCertified{}, errManagedOperational
	}
	manager.mu.Lock()
	slowEntry, rateEntry, requestEntry, backupEntry, capacityEntry, lifecycleEntry, searchEntry := manager.slowRPC, manager.rate, manager.request, manager.backup, manager.capacity, manager.lifecycle, manager.search
	manager.mu.Unlock()
	if slowEntry == nil || slowEntry.owner != slow.adapter || rateEntry == nil || rateEntry.owner != rate.adapter || requestEntry == nil || requestEntry.owner != request || backupEntry == nil || backupEntry.owner != backup.adapter || capacityEntry == nil || capacityEntry.owner != capacity.adapter || lifecycleEntry == nil || lifecycleEntry.owner != lifecycle.adapter || searchEntry == nil || searchEntry.owner != search.runtime {
		return managedOperationalCertified{}, errManagedOperational
	}
	// Availability proves installed owners, including the fixed recovery pair.
	// Invalid bootstrap values must remain repairable, so no value validation or
	// owner callback runs while holding the manager lock.
	if slow.adapter.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, operationalpolicy.Version) != nil || rate.adapter.AvailableOperationalPolicy(operationalpolicy.GroupRate, operationalpolicy.Version) != nil || request.AvailableOperationalPolicy(operationalpolicy.GroupRequest, operationalpolicy.Version) != nil || backup.adapter.AvailableOperationalPolicy(operationalpolicy.GroupBackup, operationalpolicy.Version) != nil || capacity.adapter.AvailableOperationalPolicy(operationalpolicy.GroupCapacity, operationalpolicy.Version) != nil || lifecycle.adapter.AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, operationalpolicy.Version) != nil || search.runtime.AvailableOperationalPolicy(operationalpolicy.GroupSearch, operationalpolicy.Version) != nil {
		return managedOperationalCertified{}, errManagedOperational
	}
	if err := manager.Reconcile(ctx); err != nil {
		return managedOperationalCertified{}, err
	}
	if manager.authority != nil {
		if err := manager.authority.RequireOperationalPolicyOwners(ctx); err != nil {
			return managedOperationalCertified{}, err
		}
		p, err := manager.authority.CurrentOperationalPolicies(ctx)
		if err != nil {
			return managedOperationalCertified{}, err
		}
		if _, err = manager.Observe(p); err != nil {
			return managedOperationalCertified{}, err
		}
	}
	return managedOperationalCertified{runtime: runtime, manager: manager, slowRPC: slow.owner, rate: rate.owner, recovery: rate.recovery, primary: primary, request: request, validator: request.validator, backup: backup.owner, capacity: capacity.owner, plan: lifecycle.plan, search: search}, nil
}
