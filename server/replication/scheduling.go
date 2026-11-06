package replication

import "time"

// PumpScheduling is the immutable schedule actually installed by NewPump.
// Observing it does not replace workers, reconnect peers or reset resume state.
type PumpScheduling struct {
	BackoffMin        time.Duration
	BackoffMax        time.Duration
	SearchFingerprint string
}

// Scheduling returns constructor-resolved values, including legacy defaults.
func (p *Pump) Scheduling() (PumpScheduling, bool) {
	if p == nil {
		return PumpScheduling{}, false
	}
	return PumpScheduling{p.cfg.BackoffMin, p.cfg.BackoffMax, p.cfg.SearchConfigFingerprint}, true
}

// AntiEntropyScheduling describes the actual companion worker and its Pump.
// The pointer is an identity observation, never permission to replace a worker.
type AntiEntropyScheduling struct {
	Interval          time.Duration
	SubscribeTimeout  time.Duration
	GapWarnThreshold  uint64
	SearchFingerprint string
	Pump              *Pump
}

func (a *AntiEntropy) Scheduling() (AntiEntropyScheduling, bool) {
	if a == nil {
		return AntiEntropyScheduling{}, false
	}
	return AntiEntropyScheduling{
		a.cfg.Interval, a.cfg.SubscribeTimeout, a.cfg.GapWarnThreshold,
		a.cfg.SearchConfigFingerprint, a.cfg.Pump,
	}, true
}
