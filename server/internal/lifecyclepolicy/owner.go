// Package lifecyclepolicy owns immutable effective lifecycle settings. Native
// adoption and durable desired generations remain control-plane concerns.
package lifecyclepolicy

import (
	"errors"
	"sync/atomic"
	"time"
)

// Policy contains the three atomic lifecycle settings. ShutdownTimeout bounds
// public HTTP draining only. Zero disables that deadline, not other cleanup.
type Policy struct {
	DrainDelay        time.Duration
	ShutdownTimeout   time.Duration
	MaxReplicationLag uint64
}

func (p Policy) Validate() error {
	if p.DrainDelay < 0 || p.ShutdownTimeout < 0 || p.DrainDelay%time.Second != 0 || p.ShutdownTimeout%time.Second != 0 {
		return errors.New("lifecycle delays must be nonnegative whole second durations")
	}
	return nil
}

type Snapshot struct {
	Generation uint64
	Policy     Policy
}

type Source interface {
	LifecycleSnapshot() Snapshot
}

// Owner publishes entire effective generations. Its values contain no pointers
// or authorization state. Only the management adapter installs committed
// generations in production; generation zero identifies local bootstrap.
type Owner struct {
	current atomic.Pointer[Snapshot]
}

func NewOwner(initial Snapshot) (*Owner, error) {
	if err := initial.Policy.Validate(); err != nil {
		return nil, err
	}
	o := &Owner{}
	o.current.Store(&initial)
	return o, nil
}

func (o *Owner) LifecycleSnapshot() Snapshot { return *o.current.Load() }

// Apply rejects stale and conflicting callbacks without changing any field.
// Reapplying the exact generation is idempotent; recovery may skip generations.
func (o *Owner) Apply(next Snapshot) error {
	if err := next.Policy.Validate(); err != nil {
		return err
	}
	for {
		previous := o.current.Load()
		if next.Generation < previous.Generation || next.Generation == previous.Generation && next.Policy != previous.Policy {
			return errors.New("lifecycle effective generation conflict")
		}
		if next == *previous || o.current.CompareAndSwap(previous, &next) {
			return nil
		}
	}
}
