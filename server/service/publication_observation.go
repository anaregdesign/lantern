package service

import (
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// publicationObserver is an optional, bounded extension of HotPathMetrics.
// Observations contain fixed categories only. Callbacks run after the service
// publication gate is released; they must not call back into this service.
type publicationObserver interface {
	OnPublicationGate(category string, wait, body, unlock time.Duration)
	OnPublicationRead(attempt int, outcome string, capture time.Duration)
	OnPublicationApply(delivery, outcome string, preflight time.Duration, acquired bool, committed uint64, pending int)
}

// Scrapes read service-owned atomics without taking the publication gate or
// invoking the metrics sink while an acquisition is in progress.
type publicationWaitingCounters struct {
	values [10]atomic.Int64
}

func (w *publicationWaitingCounters) counter(category string) *atomic.Int64 {
	index := 9
	switch category {
	case "local_put":
		index = 0
	case "local_add":
		index = 1
	case "local_delete":
		index = 2
	case "remote_apply":
		index = 3
	case "generation_probe":
		index = 4
	case "read_fallback":
		index = 5
	case "committed_view":
		index = 6
	case "source_cut":
		index = 7
	case "snapshot":
		index = 8
	}
	return &w.values[index]
}

func (w *publicationWaitingCounters) count(category string) int64 {
	return w.counter(category).Load()
}

type publicationGateObservation struct {
	gate                *publicationGate
	observer            publicationObserver
	category            string
	read                bool
	now                 func() time.Time
	requested, acquired time.Time
}

// The four monotonic samples distinguish acquisition wait, owner body and
// Unlock/handoff. In particular, Unlock time is not counted as owner work.
func (s *LanternService) lockPublicationGate(category string, read bool) publicationGateObservation {
	o := publicationGateObservation{gate: &s.replicationCutMu, observer: s.publicationObserver, category: category, read: read}
	if o.observer != nil {
		s.publicationWaiting.counter(category).Add(1)
		o.now = s.publicationTime
		o.requested = o.now()
	}
	if read {
		o.gate.RLock()
	} else {
		o.gate.Lock()
	}
	if o.observer != nil {
		o.acquired = o.now()
		s.publicationWaiting.counter(category).Add(-1)
	}
	return o
}

func (o publicationGateObservation) Unlock() {
	var before time.Time
	if o.observer != nil {
		before = o.now()
	}
	if o.read {
		o.gate.RUnlock()
	} else {
		// Keep the existing generation advance and mutex release together.
		o.gate.Unlock()
	}
	if o.observer != nil {
		after := o.now()
		o.observer.OnPublicationGate(o.category, o.acquired.Sub(o.requested), before.Sub(o.acquired), after.Sub(before))
	}
}

func (s *LanternService) publicationTime() time.Time {
	if s.publicationClock != nil {
		return s.publicationClock()
	}
	return time.Now()
}

func (s *LanternService) publicationCaptureStart() time.Time {
	if s.publicationObserver == nil {
		return time.Time{}
	}
	return s.publicationTime()
}

func (s *LanternService) observePublicationRead(attempt int, outcome string, start, end time.Time) {
	if s.publicationObserver != nil {
		s.publicationObserver.OnPublicationRead(attempt, outcome, end.Sub(start))
	}
}

type publicationApplyObservation struct {
	observer           publicationObserver
	now                func() time.Time
	started, preflight time.Time
	delivery, outcome  string
	acquired           bool
	origin             hlc.NodeID
	validOrigin        bool
	before, committed  uint64
	pending            int
}

func (s *LanternService) beginPublicationApply() publicationApplyObservation {
	o := publicationApplyObservation{observer: s.publicationObserver, delivery: "unclassified", outcome: "rejected"}
	if o.observer != nil {
		o.now = s.publicationTime
		o.started = o.now()
	}
	return o
}

func (o *publicationApplyObservation) preflightDone() {
	if o.observer != nil {
		o.preflight = o.now()
	}
}

func (o *publicationApplyObservation) beforePublish(s *LanternService, m *pb.Mutation) {
	if o.observer == nil || s.origins == nil || len(m.GetOrigin()) != len(o.origin) {
		return
	}
	copy(o.origin[:], m.GetOrigin())
	o.validOrigin = true
	o.before = s.origins.LocalSeq(o.origin)
	switch {
	case m.GetSeq() <= o.before:
		o.delivery = "committed_duplicate"
	case s.pendingMutations[o.origin][m.GetSeq()] != nil:
		o.delivery = "pending_duplicate"
	default:
		o.delivery = "new_sequence"
	}
}

func (o *publicationApplyObservation) afterPublish(s *LanternService, err error) {
	if o.observer == nil {
		return
	}
	o.pending = s.pendingCount
	if o.validOrigin {
		if after := s.origins.LocalSeq(o.origin); after >= o.before {
			// Under the unchanged exclusive gate, contiguous frontier advance
			// counts committed events, including a multi-entry pending drain.
			o.committed = after - o.before
		}
	}
	if err != nil {
		return
	}
	switch {
	case o.committed > 0:
		o.outcome = "committed"
	case o.delivery == "committed_duplicate":
		o.outcome = "committed_duplicate"
	case o.delivery == "pending_duplicate":
		o.outcome = "pending_duplicate"
	default:
		o.outcome = "queued"
	}
}

func (o publicationApplyObservation) finish(err error) {
	if o.observer == nil {
		return
	}
	if o.preflight.IsZero() {
		o.preflight = o.now()
	}
	if connect.CodeOf(err) == connect.CodeCanceled {
		o.outcome = "cancelled"
	}
	o.observer.OnPublicationApply(o.delivery, o.outcome, o.preflight.Sub(o.started), o.acquired, o.committed, o.pending)
}
