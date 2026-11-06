package provider

import (
	"context"
	"log/slog"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
	"github.com/anaregdesign/lantern/server/replication"
)

// managedReplicationCertified owns the exact immutable worker pair used by App
// and status. Construction creates no worker goroutine; App retains Run ownership.
type managedReplicationCertified struct {
	manager *ManagedOperationalRuntime
	primary *dataruntime.Executor
	owner   *managedReplicationOwner
	pump    *replication.Pump
	anti    *replication.AntiEntropy
}

type managedReplicationOwner struct {
	*replicationPolicyRuntime
	sources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
	binding searchPumpCertified
}

func (c managedReplicationCertified) validFor(manager *ManagedOperationalRuntime, primary *dataruntime.Executor, search managedSearchCertified) bool {
	if manager == nil || c.manager != manager || c.primary != primary || c.owner == nil || c.owner.replicationPolicyRuntime == nil || c.owner.pump != c.pump || c.owner.anti != c.anti {
		return false
	}
	binding := c.owner.binding
	if !binding.validFor(binding.certified, manager, primary, binding.cache, search, c.pump) || c.anti == nil || !c.anti.UsesPump(c.pump) || c.anti.SearchConfigFingerprint() != binding.fingerprint {
		return false
	}
	manager.mu.Lock()
	entry := manager.replication
	manager.mu.Unlock()
	return entry != nil && entry.owner == c.owner && c.owner.AvailableOperationalPolicy(operationalpolicy.GroupReplication, operationalpolicy.Version) == nil
}

// NewManagedReplicationRuntime restores the selected schedule before creating
// workers. The Search dependency proves the final installed engine, so neither
// worker can capture a pre-restore fingerprint. External peer bindings survive.
func NewManagedReplicationRuntime(
	ctx context.Context,
	manager *ManagedOperationalRuntime,
	peer PeerConfig,
	antiConfig AntiEntropyConfig,
	resolver *PeerResolver,
	identity ReplicationConfig,
	transport *replication.PeerTransport,
	primary *dataruntime.Executor,
	cache *graphcache.GraphCache[string, *pb.Vertex],
	pumpMetrics replication.Metrics,
	antiMetrics replication.AntiEntropyMetrics,
	logger *slog.Logger,
	installer *SnapshotInstallerSelection,
	certified runtimeCertified,
	search managedSearchCertified,
) (managedReplicationCertified, error) {
	if manager == nil || manager.runtime == nil || primary == nil || !certified.valid || certified.primary != primary || certified.runtime != manager.runtime.data || certified.replication == nil || !primary.SharesServingRuntime(certified.runtime) || cache == nil || cache != NewRuntimeGraph(certified.runtime) || !search.validFor(manager, primary) || ctx.Err() != nil {
		return managedReplicationCertified{}, errManagedReplication
	}
	sources, err := manager.BootstrapSources(operationalpolicy.GroupReplication)
	if err != nil {
		return managedReplicationCertified{}, err
	}
	var boot *operationalpolicy.Policy
	if manager.authority != nil {
		projection, readErr := manager.authority.CurrentOperationalPolicies(ctx)
		if readErr != nil || validateOperationalProjection(projection) != nil {
			return managedReplicationCertified{}, errManagedReplication
		}
		boot = projection.Policies.Get(operationalpolicy.GroupReplication)
	}
	resolvedPeer, resolvedAnti := peer, antiConfig
	if boot != nil {
		if boot.Validate() != nil || boot.Values.Replication == nil {
			return managedReplicationCertified{}, errManagedReplication
		}
		resolvedPeer, resolvedAnti, err = resolveReplicationPolicy(*boot.Values.Replication, peer, antiConfig)
		if err != nil {
			return managedReplicationCertified{}, err
		}
	}
	fingerprint := primary.SearchConfigFingerprint()
	if fingerprint == "" {
		return managedReplicationCertified{}, errManagedReplication
	}
	// Reuse the concrete Search witnesses inside this one bundle. Only the
	// managed selectors participate in Wire; no second worker pair is built.
	binding, err := NewSearchCertifiedReplicationPump(resolvedPeer, resolver, identity, transport, primary, cache, pumpMetrics, logger, installer, certified, manager, search)
	if err != nil {
		return managedReplicationCertified{}, err
	}
	pump, err := NewSearchReplicationPump(binding)
	if err != nil {
		return managedReplicationCertified{}, err
	}
	anti, err := NewSearchCertifiedAntiEntropyDriver(resolvedPeer, resolver, identity, resolvedAnti, transport, primary, cache, pump, antiMetrics, logger, installer, certified, manager, search, binding)
	if err != nil {
		return managedReplicationCertified{}, err
	}
	pumpActual, pOK := pump.Scheduling()
	antiActual, aOK := anti.Scheduling()
	if !pOK || !aOK || pumpActual.SearchFingerprint != fingerprint || antiActual.SearchFingerprint != fingerprint || !search.validFor(manager, primary) {
		return managedReplicationCertified{}, errManagedReplication
	}
	runtime, err := newReplicationPolicyRuntime(pump, anti, boot)
	if err != nil {
		return managedReplicationCertified{}, err
	}
	if boot == nil {
		markReplicationDerived(sources, peer, antiConfig, pumpActual, antiActual)
	}
	owner := &managedReplicationOwner{replicationPolicyRuntime: runtime, sources: sources, binding: binding}
	if err := manager.Register(ctx, operationalpolicy.GroupReplication, owner); err != nil {
		return managedReplicationCertified{}, err
	}
	result := managedReplicationCertified{manager, primary, owner, pump, anti}
	if !result.validFor(manager, primary, search) {
		return managedReplicationCertified{}, errManagedReplication
	}
	return result, nil
}

func markReplicationDerived(sources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource, peer PeerConfig, antiConfig AntiEntropyConfig, pump replication.PumpScheduling, anti replication.AntiEntropyScheduling) {
	for _, field := range []struct {
		key     securitytransport.OperationalSetting
		changed bool
	}{
		{pb.OperationalSetting_OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MIN_MS, peer.BackoffMin != pump.BackoffMin},
		{pb.OperationalSetting_OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MAX_MS, peer.BackoffMax != pump.BackoffMax},
		{pb.OperationalSetting_OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_INTERVAL_MS, antiConfig.Interval != anti.Interval},
		{pb.OperationalSetting_OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS, antiConfig.SubscribeTimeout != anti.SubscribeTimeout},
		{pb.OperationalSetting_OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_GAP_WARN_THRESHOLD, antiConfig.GapWarnThreshold != anti.GapWarnThreshold},
	} {
		if field.changed {
			sources[field.key] = securitytransport.OperationalSourceDerived
		}
	}
}

func (o *managedReplicationOwner) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if o == nil || o.replicationPolicyRuntime == nil {
		return OperationalOwnerObservation{}, errManagedReplication
	}
	actual, err := o.observation()
	result := OperationalOwnerObservation{
		Effective: actual.Effective, EffectiveGeneration: actual.AppliedGeneration,
		Sources: cloneOperationalSources(o.sources), Mode: securitytransport.OperationalModeProcessRestart,
		Error: securitytransport.OperationalErrorNone,
	}
	// Search fingerprint is an internal pair-binding check above. The public
	// operational fingerprint field is reserved for the Search group only.
	if err != nil {
		if o.AvailableOperationalPolicy(operationalpolicy.GroupReplication, operationalpolicy.Version) != nil {
			return OperationalOwnerObservation{}, err
		}
		result.Error = securitytransport.OperationalErrorInvalidPolicy
		return result, nil
	}
	if actual.AppliedGeneration != 0 {
		for field := range result.Sources {
			result.Sources[field] = securitytransport.OperationalSourceManaged
		}
	}
	return result, nil
}

// NewManagedReplicationPump returns the exact constructed worker for App and
// status without replacing it or resetting its resume state.
func NewManagedReplicationPump(certified managedReplicationCertified) (*replication.Pump, error) {
	if certified.owner == nil || !certified.validFor(certified.manager, certified.primary, certified.owner.binding.search) {
		return nil, errManagedReplication
	}
	return certified.pump, nil
}

// NewManagedAntiEntropyDriver returns the actual companion of that same Pump.
func NewManagedAntiEntropyDriver(certified managedReplicationCertified) (*replication.AntiEntropy, error) {
	if certified.owner == nil || !certified.validFor(certified.manager, certified.primary, certified.owner.binding.search) {
		return nil, errManagedReplication
	}
	return certified.anti, nil
}

// UsesManagedReplicationWorkers verifies the actual registered worker pair
// before App binds status or starts workers. Equal fingerprints never replace
// pointer identity, and the check performs no authority read or worker action.
func (r *SecurityRuntime) UsesManagedReplicationWorkers(primary *dataruntime.Executor, pump *replication.Pump, anti *replication.AntiEntropy) bool {
	if r == nil || r.managedOperational == nil || r.managedOperational.runtime != r {
		return false
	}
	manager := r.managedOperational
	manager.mu.Lock()
	entry := manager.replication
	manager.mu.Unlock()
	if entry == nil {
		return false
	}
	owner, ok := entry.owner.(*managedReplicationOwner)
	if !ok || owner == nil {
		return false
	}
	actual := managedReplicationCertified{manager, primary, owner, pump, anti}
	return actual.validFor(manager, primary, owner.binding.search)
}
