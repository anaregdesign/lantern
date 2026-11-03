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
	pump := NewReplicationPump(resolvedPeer, resolver, identity, transport, primary, cache, pumpMetrics, logger, installer, certified)
	anti := NewAntiEntropyDriver(resolvedPeer, resolver, identity, resolvedAnti, transport, primary, cache, pump, antiMetrics, logger, installer)
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
	owner := &managedReplicationOwner{replicationPolicyRuntime: runtime, sources: sources}
	if err := manager.Register(ctx, operationalpolicy.GroupReplication, owner); err != nil {
		return managedReplicationCertified{}, err
	}
	return managedReplicationCertified{manager, primary, owner, pump, anti}, nil
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
func NewManagedReplicationPump(certified managedReplicationCertified) *replication.Pump {
	return certified.pump
}

// NewManagedAntiEntropyDriver returns the actual companion of that same Pump.
func NewManagedAntiEntropyDriver(certified managedReplicationCertified) *replication.AntiEntropy {
	return certified.anti
}
