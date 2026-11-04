package securitytransport

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"reflect"
)

func decodeOperationalValues(wire *pb.OperationalPolicyValues) (operationalpolicy.Values, error) {
	var values operationalpolicy.Values
	if wire == nil || strictSecurityMessage(wire) != nil || wire.Group == nil || reflect.ValueOf(wire.Group).IsNil() {
		return values, security.ErrInvalidImage
	}
	switch arm := wire.Group.(type) {
	case *pb.OperationalPolicyValues_SlowRpc:
		if arm.SlowRpc == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.SlowRpc
		values.SlowRPC = &operationalpolicy.SlowRPC{
			ThresholdMS: v.ThresholdMs,
		}
	case *pb.OperationalPolicyValues_Rate:
		if arm.Rate == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.Rate
		values.Rate = &operationalpolicy.Rate{
			RPS:   v.Rps,
			Burst: v.Burst,
		}
	case *pb.OperationalPolicyValues_Request:
		if arm.Request == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.Request
		values.Request = &operationalpolicy.Request{
			DeleteByPrefixDefaultLimit: v.DeleteByPrefixDefaultLimit,
			DeleteByPrefixMaxLimit:     v.DeleteByPrefixMaxLimit,
			IlluminateMaxK:             v.IlluminateMaxK,
			IlluminateMaxStep:          v.IlluminateMaxStep,
			MaxBatchSize:               v.MaxBatchSize,
			MaxKeyLen:                  v.MaxKeyLen,
			ScanDefaultLimit:           v.ScanDefaultLimit,
			ScanMaxLimit:               v.ScanMaxLimit,
			TraversalMaxPushes:         v.TraversalMaxPushes,
			TraversalMaxResults:        v.TraversalMaxResults,
			TraversalMaxTouchedEdges:   v.TraversalMaxTouchedEdges,
			TraversalTimeoutMS:         v.TraversalTimeoutMs,
		}
	case *pb.OperationalPolicyValues_Backup:
		if arm.Backup == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.Backup
		values.Backup = &operationalpolicy.Backup{
			Enabled:       v.Enabled,
			IntervalNanos: v.IntervalNanos,
			Retain:        v.Retain,
		}
	case *pb.OperationalPolicyValues_Capacity:
		if arm.Capacity == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.Capacity
		values.Capacity = &operationalpolicy.Capacity{
			GCEdgeBudget:           v.GcEdgeBudget,
			GCIntervalSeconds:      v.GcIntervalSeconds,
			MaxEdges:               v.MaxEdges,
			MaxEdgeCausalEntries:   v.MaxEdgeCausalEntries,
			MaxVertexCausalEntries: v.MaxVertexCausalEntries,
			MaxVertices:            v.MaxVertices,
		}
	case *pb.OperationalPolicyValues_Lifecycle:
		if arm.Lifecycle == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.Lifecycle
		values.Lifecycle = &operationalpolicy.Lifecycle{
			DrainDelaySeconds:      v.DrainDelaySeconds,
			MaxReplicationLag:      v.MaxReplicationLag,
			ShutdownTimeoutSeconds: v.ShutdownTimeoutSeconds,
		}
	case *pb.OperationalPolicyValues_Search:
		if arm.Search == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.Search
		values.Search = &operationalpolicy.Search{
			DefaultLimit:        v.DefaultLimit,
			MaxLimit:            v.MaxLimit,
			DefaultMode:         decodeOperationalSearchMode(v.DefaultMode),
			DefaultMinShould:    v.DefaultMinShould,
			TimeoutMS:           v.TimeoutMs,
			MaxQueryBytes:       v.MaxQueryBytes,
			MaxQueryTerms:       v.MaxQueryTerms,
			MaxDictionaryVisits: v.MaxDictionaryVisits,
			MaxPostingVisits:    v.MaxPostingVisits,
			MaxPositionVisits:   v.MaxPositionVisits,
			MaxExpirationVisits: v.MaxExpirationVisits,
		}
	case *pb.OperationalPolicyValues_Replication:
		if arm.Replication == nil {
			return values, security.ErrInvalidImage
		}
		v := arm.Replication
		values.Replication = &operationalpolicy.Replication{
			AntiEntropyGapWarnThreshold:   v.AntiEntropyGapWarnThreshold,
			AntiEntropyIntervalMS:         v.AntiEntropyIntervalMs,
			AntiEntropySubscribeTimeoutMS: v.AntiEntropySubscribeTimeoutMs,
			PumpBackoffMaxMS:              v.PumpBackoffMaxMs,
			PumpBackoffMinMS:              v.PumpBackoffMinMs,
		}
	default:
		return values, security.ErrInvalidImage
	}
	if values.Validate() != nil {
		return values, security.ErrInvalidImage
	}
	return values, nil
}

func encodeOperationalValues(values operationalpolicy.Values) *pb.OperationalPolicyValues {
	wire := &pb.OperationalPolicyValues{}
	group, err := values.Group()
	if err != nil {
		return nil
	}
	switch group {
	case operationalpolicy.GroupSlowRPC:
		v := values.SlowRPC
		wire.Group = &pb.OperationalPolicyValues_SlowRpc{SlowRpc: &pb.OperationalSlowRPCValues{
			ThresholdMs: v.ThresholdMS,
		}}
	case operationalpolicy.GroupRate:
		v := values.Rate
		wire.Group = &pb.OperationalPolicyValues_Rate{Rate: &pb.OperationalRateValues{
			Rps:   v.RPS,
			Burst: v.Burst,
		}}
	case operationalpolicy.GroupRequest:
		v := values.Request
		wire.Group = &pb.OperationalPolicyValues_Request{Request: &pb.OperationalRequestValues{
			DeleteByPrefixDefaultLimit: v.DeleteByPrefixDefaultLimit,
			DeleteByPrefixMaxLimit:     v.DeleteByPrefixMaxLimit,
			IlluminateMaxK:             v.IlluminateMaxK,
			IlluminateMaxStep:          v.IlluminateMaxStep,
			MaxBatchSize:               v.MaxBatchSize,
			MaxKeyLen:                  v.MaxKeyLen,
			ScanDefaultLimit:           v.ScanDefaultLimit,
			ScanMaxLimit:               v.ScanMaxLimit,
			TraversalMaxPushes:         v.TraversalMaxPushes,
			TraversalMaxResults:        v.TraversalMaxResults,
			TraversalMaxTouchedEdges:   v.TraversalMaxTouchedEdges,
			TraversalTimeoutMs:         v.TraversalTimeoutMS,
		}}
	case operationalpolicy.GroupBackup:
		v := values.Backup
		wire.Group = &pb.OperationalPolicyValues_Backup{Backup: &pb.OperationalBackupValues{
			Enabled:       v.Enabled,
			IntervalNanos: v.IntervalNanos,
			Retain:        v.Retain,
		}}
	case operationalpolicy.GroupCapacity:
		v := values.Capacity
		wire.Group = &pb.OperationalPolicyValues_Capacity{Capacity: &pb.OperationalCapacityValues{
			GcEdgeBudget:           v.GCEdgeBudget,
			GcIntervalSeconds:      v.GCIntervalSeconds,
			MaxEdges:               v.MaxEdges,
			MaxEdgeCausalEntries:   v.MaxEdgeCausalEntries,
			MaxVertexCausalEntries: v.MaxVertexCausalEntries,
			MaxVertices:            v.MaxVertices,
		}}
	case operationalpolicy.GroupLifecycle:
		v := values.Lifecycle
		wire.Group = &pb.OperationalPolicyValues_Lifecycle{Lifecycle: &pb.OperationalLifecycleValues{
			DrainDelaySeconds:      v.DrainDelaySeconds,
			MaxReplicationLag:      v.MaxReplicationLag,
			ShutdownTimeoutSeconds: v.ShutdownTimeoutSeconds,
		}}
	case operationalpolicy.GroupSearch:
		v := values.Search
		wire.Group = &pb.OperationalPolicyValues_Search{Search: &pb.OperationalSearchValues{
			DefaultLimit:        v.DefaultLimit,
			MaxLimit:            v.MaxLimit,
			DefaultMode:         encodeOperationalSearchMode(v.DefaultMode),
			DefaultMinShould:    v.DefaultMinShould,
			TimeoutMs:           v.TimeoutMS,
			MaxQueryBytes:       v.MaxQueryBytes,
			MaxQueryTerms:       v.MaxQueryTerms,
			MaxDictionaryVisits: v.MaxDictionaryVisits,
			MaxPostingVisits:    v.MaxPostingVisits,
			MaxPositionVisits:   v.MaxPositionVisits,
			MaxExpirationVisits: v.MaxExpirationVisits,
		}}
	case operationalpolicy.GroupReplication:
		v := values.Replication
		wire.Group = &pb.OperationalPolicyValues_Replication{Replication: &pb.OperationalReplicationValues{
			AntiEntropyGapWarnThreshold:   v.AntiEntropyGapWarnThreshold,
			AntiEntropyIntervalMs:         v.AntiEntropyIntervalMS,
			AntiEntropySubscribeTimeoutMs: v.AntiEntropySubscribeTimeoutMS,
			PumpBackoffMaxMs:              v.PumpBackoffMaxMS,
			PumpBackoffMinMs:              v.PumpBackoffMinMS,
		}}
	}
	return wire
}

func decodeOperationalSearchMode(mode pb.OperationalSearchMode) operationalpolicy.MatchMode {
	switch mode {
	case pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_ANY:
		return operationalpolicy.MatchAny
	case pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_ALL:
		return operationalpolicy.MatchAll
	case pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_MIN_SHOULD:
		return operationalpolicy.MatchMinShould
	default:
		return ""
	}
}
func encodeOperationalSearchMode(mode operationalpolicy.MatchMode) pb.OperationalSearchMode {
	switch mode {
	case operationalpolicy.MatchAny:
		return pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_ANY
	case operationalpolicy.MatchAll:
		return pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_ALL
	case operationalpolicy.MatchMinShould:
		return pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_MIN_SHOULD
	default:
		return pb.OperationalSearchMode_OPERATIONAL_SEARCH_MODE_UNSPECIFIED
	}
}

func decodeOperationalPolicy(wire *pb.OperationalPolicyChange) (*operationalpolicy.Change, error) {
	if wire == nil || strictSecurityMessage(wire) != nil || wire.Version != operationalpolicy.Version {
		return nil, security.ErrInvalidImage
	}
	values, err := decodeOperationalValues(wire.Values)
	if err != nil {
		return nil, err
	}
	change := &operationalpolicy.Change{ExpectedGeneration: wire.ExpectedGeneration, Values: values, CompatibilityFingerprint: wire.CompatibilityFingerprint, RetentionReductionAcknowledged: wire.RetentionReductionAcknowledged}
	if change.Validate() != nil {
		return nil, security.ErrInvalidImage
	}
	return change, nil
}

func encodeOperationalGroup(group operationalpolicy.Group) pb.OperationalGroup {
	switch group {
	case operationalpolicy.GroupSlowRPC:
		return pb.OperationalGroup_OPERATIONAL_GROUP_SLOW_RPC
	case operationalpolicy.GroupRate:
		return pb.OperationalGroup_OPERATIONAL_GROUP_RATE
	case operationalpolicy.GroupRequest:
		return pb.OperationalGroup_OPERATIONAL_GROUP_REQUEST
	case operationalpolicy.GroupBackup:
		return pb.OperationalGroup_OPERATIONAL_GROUP_BACKUP
	case operationalpolicy.GroupCapacity:
		return pb.OperationalGroup_OPERATIONAL_GROUP_CAPACITY
	case operationalpolicy.GroupLifecycle:
		return pb.OperationalGroup_OPERATIONAL_GROUP_LIFECYCLE
	case operationalpolicy.GroupSearch:
		return pb.OperationalGroup_OPERATIONAL_GROUP_SEARCH
	case operationalpolicy.GroupReplication:
		return pb.OperationalGroup_OPERATIONAL_GROUP_REPLICATION
	default:
		return pb.OperationalGroup_OPERATIONAL_GROUP_UNSPECIFIED
	}
}
