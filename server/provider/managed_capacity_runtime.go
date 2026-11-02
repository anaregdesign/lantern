package provider

import (
	"context"
	"errors"
	"math"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

type managedCapacityCertified struct {
	manager *ManagedOperationalRuntime
	owner   *dataruntime.GraphPolicyOwner
	adapter *managedCapacityRuntime
}

type managedCapacityRuntime struct {
	owner            *dataruntime.GraphPolicyOwner
	bootstrapSources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
}

func NewManagedCapacityRuntime(ctx context.Context, manager *ManagedOperationalRuntime, owner *dataruntime.GraphPolicyOwner) (managedCapacityCertified, error) {
	if ctx.Err() != nil || manager == nil {
		return managedCapacityCertified{}, errManagedOperational
	}
	sources, err := manager.BootstrapSources(operationalpolicy.GroupCapacity)
	if err != nil {
		return managedCapacityCertified{}, errManagedOperational
	}
	adapter := &managedCapacityRuntime{owner: owner, bootstrapSources: sources}
	if err := adapter.AvailableOperationalPolicy(operationalpolicy.GroupCapacity, operationalpolicy.Version); err != nil {
		return managedCapacityCertified{}, err
	}
	if err := manager.Register(ctx, operationalpolicy.GroupCapacity, adapter); err != nil {
		return managedCapacityCertified{}, err
	}
	return managedCapacityCertified{manager, owner, adapter}, nil
}

func (c *managedCapacityRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if c == nil || c.owner == nil || group != operationalpolicy.GroupCapacity || version != operationalpolicy.Version || c.owner.AvailableGraphPolicy() != nil {
		return errManagedOperational
	}
	return nil
}

func managedCapacityConfig(values operationalpolicy.Values) (dataruntime.GraphPolicyConfig, error) {
	if values.Validate() != nil || values.Capacity == nil {
		return dataruntime.GraphPolicyConfig{}, errManagedOperational
	}
	value := values.Capacity
	for _, count := range []int64{value.GCEdgeBudget, value.MaxEdges, value.MaxEdgeCausalEntries, value.MaxVertexCausalEntries, value.MaxVertices} {
		if count < 0 || uint64(count) > uint64(^uint(0)>>1) {
			return dataruntime.GraphPolicyConfig{}, errManagedOperational
		}
	}
	if value.GCIntervalSeconds <= 0 || value.GCIntervalSeconds > math.MaxInt64/int64(time.Second) {
		return dataruntime.GraphPolicyConfig{}, errManagedOperational
	}
	return dataruntime.GraphPolicyConfig{
		MaxVertices: int(value.MaxVertices), MaxEdges: int(value.MaxEdges),
		MaxVertexCausalEntries: int(value.MaxVertexCausalEntries), MaxEdgeCausalEntries: int(value.MaxEdgeCausalEntries),
		GCEdgeBudget: int(value.GCEdgeBudget), GCInterval: time.Duration(value.GCIntervalSeconds) * time.Second,
	}, nil
}

func (c *managedCapacityRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if c.AvailableOperationalPolicy(operationalpolicy.GroupCapacity, operationalpolicy.Version) != nil || change.Validate() != nil {
		return errManagedOperational
	}
	config, err := managedCapacityConfig(change.Values)
	if err != nil || config.Validate() != nil {
		return errManagedOperational
	}
	return nil
}

func (c *managedCapacityRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if c.AvailableOperationalPolicy(operationalpolicy.GroupCapacity, operationalpolicy.Version) != nil || policy.Validate() != nil {
		return errManagedOperational
	}
	config, err := managedCapacityConfig(policy.Values)
	if err != nil {
		return err
	}
	return c.owner.Apply(dataruntime.GraphPolicySnapshot{Generation: policy.Generation, Config: config})
}

func (c *managedCapacityRuntime) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	if c == nil || c.owner == nil {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	available := c.owner.AvailableGraphPolicy()
	if available != nil && !errors.Is(available, dataruntime.ErrGraphPolicyStopped) {
		return OperationalOwnerObservation{}, errManagedOperational
	}
	actual := c.owner.Snapshot()
	observation := OperationalOwnerObservation{EffectiveGeneration: actual.Generation, Mode: securitytransport.OperationalModeWorkerRestart, Error: securitytransport.OperationalErrorNone}
	if actual.Generation == 0 {
		if len(c.bootstrapSources) != 6 {
			return observation, errManagedOperational
		}
		observation.Sources = cloneOperationalSources(c.bootstrapSources)
	} else {
		observation.Sources = make(map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource, 6)
		for field := pb.OperationalSetting_OPERATIONAL_SETTING_CAPACITY_GC_EDGE_BUDGET; field <= pb.OperationalSetting_OPERATIONAL_SETTING_CAPACITY_MAX_VERTICES; field++ {
			observation.Sources[field] = securitytransport.OperationalSourceManaged
		}
	}
	config := actual.Config
	if config.GCInterval <= 0 || config.GCInterval%time.Second != 0 {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	values := operationalpolicy.Values{Capacity: &operationalpolicy.Capacity{
		MaxVertices: int64(config.MaxVertices), MaxEdges: int64(config.MaxEdges),
		MaxVertexCausalEntries: int64(config.MaxVertexCausalEntries), MaxEdgeCausalEntries: int64(config.MaxEdgeCausalEntries),
		GCEdgeBudget: int64(config.GCEdgeBudget), GCIntervalSeconds: int64(config.GCInterval / time.Second),
	}}
	if values.Validate() != nil {
		observation.Error = securitytransport.OperationalErrorInvalidPolicy
		return observation, nil
	}
	observation.Effective = &values
	if available != nil {
		observation.Error = securitytransport.OperationalErrorOwnerUnavailable
	}
	return observation, nil
}
