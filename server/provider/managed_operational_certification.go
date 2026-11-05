package provider

import (
	"context"

	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
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
}

func NewManagedOperationalCertified(ctx context.Context, runtime *SecurityRuntime, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, slow managedSlowRPCCertified, rate managedRateCertified, request *ManagedRequestRuntime) (managedOperationalCertified, error) {
	if runtime == nil || manager == nil || manager.runtime != runtime || runtime.managedOperational != manager || slow.manager != manager || slow.owner == nil || slow.adapter == nil || slow.adapter.owner != slow.owner {
		return managedOperationalCertified{}, errManagedOperational
	}
	if rate.manager != manager || rate.owner == nil || rate.adapter == nil || rate.adapter.manager != manager || rate.adapter.owner != rate.owner || rate.adapter.recovery != rate.recovery || !rate.recovery.bound(runtime, rate.owner) {
		return managedOperationalCertified{}, errManagedOperational
	}
	if primary == nil || !primary.SharesServingRuntime(runtime.data) || request == nil || request.manager != manager || request.executor != primary || request.owner == nil || request.validator == nil || request.validator.policies != request.owner || !primary.UsesRequestPolicyOwner(request.owner) {
		return managedOperationalCertified{}, errManagedOperational
	}
	manager.mu.Lock()
	slowEntry, rateEntry, requestEntry := manager.slowRPC, manager.rate, manager.request
	manager.mu.Unlock()
	if slowEntry == nil || slowEntry.owner != slow.adapter || rateEntry == nil || rateEntry.owner != rate.adapter || requestEntry == nil || requestEntry.owner != request {
		return managedOperationalCertified{}, errManagedOperational
	}
	// Availability proves installed owners, including the fixed recovery pair.
	// Invalid bootstrap values must remain repairable, so no value validation or
	// owner callback runs while holding the manager lock.
	if slow.adapter.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, operationalpolicy.Version) != nil || rate.adapter.AvailableOperationalPolicy(operationalpolicy.GroupRate, operationalpolicy.Version) != nil || request.AvailableOperationalPolicy(operationalpolicy.GroupRequest, operationalpolicy.Version) != nil {
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
	return managedOperationalCertified{runtime: runtime, manager: manager, slowRPC: slow.owner, rate: rate.owner, recovery: rate.recovery, primary: primary, request: request, validator: request.validator}, nil
}
