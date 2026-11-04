package provider

import "context"

// A concrete group marker forces the real owner to register before public
// certification. Future group packages add their own marker dependencies here;
// absent groups never obtain a dummy application capability.
type managedOperationalCertified struct {
	runtime *SecurityRuntime
	manager *ManagedOperationalRuntime
	slowRPC *SlowRPCInterceptor
}

func NewManagedOperationalCertified(ctx context.Context, runtime *SecurityRuntime, manager *ManagedOperationalRuntime, slow managedSlowRPCCertified) (managedOperationalCertified, error) {
	if runtime == nil || manager == nil || manager.runtime != runtime || runtime.managedOperational != manager || slow.manager != manager || slow.owner == nil || slow.adapter == nil || slow.adapter.owner != slow.owner {
		return managedOperationalCertified{}, errManagedOperational
	}
	manager.mu.Lock()
	entry := manager.slowRPC
	manager.mu.Unlock()
	if entry == nil || entry.owner != slow.adapter {
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
	return managedOperationalCertified{runtime, manager, slow.owner}, nil
}
