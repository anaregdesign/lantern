package provider

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/replication"
)

var errManagedReplication = errors.New("managed replication schedule unavailable")

// replicationPolicyRuntime retains the actual worker pair for this process.
// A committed successor is desired only: neither Apply nor observation mutates
// live sessions, trackers, resume state or the constructors' immutable configs.
type replicationPolicyRuntime struct {
	mu      sync.Mutex
	pump    *replication.Pump
	anti    *replication.AntiEntropy
	applied *operationalpolicy.Policy
	desired *operationalpolicy.Policy
}

type replicationPolicyObservation struct {
	Effective         *operationalpolicy.Values
	AppliedGeneration uint64
	DesiredGeneration uint64
	PendingRestart    bool
	Fingerprint       string
}

// newReplicationPolicyRuntime is called only after both workers are built
// from the locally verified boot policy and final restored Search engine.
func newReplicationPolicyRuntime(pump *replication.Pump, anti *replication.AntiEntropy, boot *operationalpolicy.Policy) (*replicationPolicyRuntime, error) {
	r := &replicationPolicyRuntime{pump: pump, anti: anti}
	if r.AvailableOperationalPolicy(operationalpolicy.GroupReplication, operationalpolicy.Version) != nil {
		return nil, errManagedReplication
	}
	if boot != nil {
		if r.PrepareOperationalPolicy(operationalpolicy.Change{Values: boot.Values.Clone()}) != nil || boot.Validate() != nil {
			return nil, errManagedReplication
		}
		actual, _, err := r.effective()
		if err != nil || *actual.Replication != *boot.Values.Replication {
			return nil, errManagedReplication
		}
		r.applied, r.desired = boot.Clone(), boot.Clone()
	}
	return r, nil
}

func (r *replicationPolicyRuntime) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if r == nil || group != operationalpolicy.GroupReplication || version != operationalpolicy.Version {
		return errManagedReplication
	}
	pump, pOK := r.pump.Scheduling()
	anti, aOK := r.anti.Scheduling()
	if !pOK || !aOK || anti.Pump != r.pump || pump.SearchFingerprint != anti.SearchFingerprint {
		return errManagedReplication
	}
	return nil
}

func (r *replicationPolicyRuntime) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupReplication, operationalpolicy.Version) != nil || change.Validate() != nil || change.Values.Replication == nil {
		return errManagedReplication
	}
	return nil
}

func (r *replicationPolicyRuntime) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	if policy.Validate() != nil || policy.Values.Replication == nil || r.AvailableOperationalPolicy(operationalpolicy.GroupReplication, operationalpolicy.Version) != nil {
		return errManagedReplication
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.desired != nil {
		if policy.Generation < r.desired.Generation {
			return errManagedReplication
		}
		if policy.Generation == r.desired.Generation {
			if *policy.Values.Replication != *r.desired.Values.Replication {
				return errManagedReplication
			}
			return nil
		}
	}
	r.desired = policy.Clone()
	return nil
}

func (r *replicationPolicyRuntime) effective() (*operationalpolicy.Values, string, error) {
	if r.AvailableOperationalPolicy(operationalpolicy.GroupReplication, operationalpolicy.Version) != nil {
		return nil, "", errManagedReplication
	}
	pump, _ := r.pump.Scheduling()
	anti, _ := r.anti.Scheduling()
	if anti.GapWarnThreshold > math.MaxInt64 || pump.BackoffMin%time.Millisecond != 0 || pump.BackoffMax%time.Millisecond != 0 || anti.Interval%time.Millisecond != 0 || anti.SubscribeTimeout%time.Millisecond != 0 {
		return nil, pump.SearchFingerprint, errManagedReplication
	}
	values := &operationalpolicy.Values{Replication: &operationalpolicy.Replication{
		AntiEntropyGapWarnThreshold:   int64(anti.GapWarnThreshold),
		AntiEntropyIntervalMS:         int64(anti.Interval / time.Millisecond),
		AntiEntropySubscribeTimeoutMS: int64(anti.SubscribeTimeout / time.Millisecond),
		PumpBackoffMaxMS:              int64(pump.BackoffMax / time.Millisecond),
		PumpBackoffMinMS:              int64(pump.BackoffMin / time.Millisecond),
	}}
	if values.Validate() != nil {
		return nil, pump.SearchFingerprint, errManagedReplication
	}
	return values, pump.SearchFingerprint, nil
}

func (r *replicationPolicyRuntime) observation() (replicationPolicyObservation, error) {
	values, fingerprint, err := r.effective()
	if r == nil {
		return replicationPolicyObservation{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := replicationPolicyObservation{Effective: values, Fingerprint: fingerprint}
	if r.applied != nil {
		result.AppliedGeneration = r.applied.Generation
	}
	if r.desired != nil {
		result.DesiredGeneration = r.desired.Generation
	}
	result.PendingRestart = result.DesiredGeneration != result.AppliedGeneration
	return result, err
}

// resolveReplicationPolicy copies only the selected five settings. External
// node, peer, transport and discovery bindings retain their existing owners.
func resolveReplicationPolicy(policy operationalpolicy.Replication, peer PeerConfig, anti AntiEntropyConfig) (PeerConfig, AntiEntropyConfig, error) {
	values := operationalpolicy.Values{Replication: &policy}
	if values.Validate() != nil {
		return PeerConfig{}, AntiEntropyConfig{}, errManagedReplication
	}
	peer.BackoffMin = time.Duration(policy.PumpBackoffMinMS) * time.Millisecond
	peer.BackoffMax = time.Duration(policy.PumpBackoffMaxMS) * time.Millisecond
	anti.Interval = time.Duration(policy.AntiEntropyIntervalMS) * time.Millisecond
	anti.SubscribeTimeout = time.Duration(policy.AntiEntropySubscribeTimeoutMS) * time.Millisecond
	anti.GapWarnThreshold = uint64(policy.AntiEntropyGapWarnThreshold)
	return peer, anti, nil
}
