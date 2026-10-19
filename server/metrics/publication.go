package metrics

import (
	"slices"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var publicationCategories = []string{
	"local_put", "local_add", "local_delete", "remote_apply", "generation_probe",
	"read_fallback", "committed_view", "source_cut", "snapshot",
}

var publicationReadOutcomes = []string{"consistent", "conflict", "capture_error", "fault"}
var publicationDeliveries = []string{"unclassified", "new_sequence", "committed_duplicate", "pending_duplicate"}
var publicationApplyOutcomes = []string{"rejected", "cancelled", "ignored", "committed", "committed_duplicate", "pending_duplicate", "queued"}

type publicationMeasurements struct {
	gate, read, preflight *prometheus.HistogramVec
	apply                 *prometheus.CounterVec
	committed, pending    prometheus.Histogram
	waiters               atomic.Pointer[publicationWaiterSource]
}

type publicationWaiterSource struct {
	read func(string) int64
}

func newPublicationMeasurements(reg prometheus.Registerer) *publicationMeasurements {
	buckets := []float64{.000001, .00001, .0001, .001, .01, .1, 1, 10, 30}
	m := &publicationMeasurements{
		gate: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_publication_gate_seconds",
			Help:    "Monotonic service publication-gate acquisition wait, owner body and Unlock/handoff durations, recorded after release. Snapshot excludes its outer admission latch wait.",
			Buckets: buckets,
		}, []string{"category", "phase"}),
		read: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_publication_read_capture_seconds",
			Help:    "Actual read captures by bounded attempt and publication-cut result; excludes protected probe waits. Fallback follows unavailable/active cuts or optimistic conflicts.",
			Buckets: buckets,
		}, []string{"attempt", "outcome"}),
		preflight: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_replication_apply_preflight_seconds",
			Help:    "Initial remote Apply validation before its optional read proof or exclusive acquisition, including rejected initial preflight attempts.",
			Buckets: buckets,
		}, []string{"delivery", "outcome"}),
		apply: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_replication_apply_attempts_total",
			Help: "Remote Apply attempts by delivery, outcome and admission stage; read_only is a validated committed duplicate without exclusive acquisition. Not a committed-event counter.",
		}, []string{"delivery", "outcome", "stage"}),
		committed: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lantern_replication_apply_committed_per_acquisition",
			Help:    "Contiguous origin events committed during one remote exclusive service-gate acquisition, including zero and pending-prefix drains; not Vertex counts.",
			Buckets: []float64{0, 1, 2, 4, 16, 64, 256, 4096},
		}),
		pending: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lantern_replication_apply_pending_at_release",
			Help:    "Pending-mutation depth sampled under the exclusive service gate at the end of each remote exclusive acquisition; not a current-depth gauge.",
			Buckets: []float64{0, 1, 2, 4, 16, 64, 256, 4096},
		}),
	}
	reg.MustRegister(m.gate, m.read, m.preflight, m.apply, m.committed, m.pending)
	for _, category := range append(append([]string{}, publicationCategories...), "unknown") {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "lantern_publication_gate_waiters",
			Help:        "Current service publication-gate acquisition attempts not yet acquired; read from atomics without taking service locks.",
			ConstLabels: prometheus.Labels{"category": category},
		}, func() float64 {
			if source := m.waiters.Load(); source != nil {
				return float64(source.read(category))
			}
			return 0
		}))
	}
	for _, category := range publicationCategories {
		for _, phase := range []string{"wait", "body", "unlock"} {
			m.gate.WithLabelValues(category, phase)
		}
	}
	return m
}

// BindPublicationWaiters installs the service's atomic-only scrape source.
// Like the other DomainMetrics samplers, one metrics instance has one owner.
func (m *DomainMetrics) BindPublicationWaiters(read func(string) int64) {
	if m != nil && m.publication != nil && read != nil {
		m.publication.waiters.Store(&publicationWaiterSource{read: read})
	}
}

func publicationLabel(value string, allowed []string) string {
	if slices.Contains(allowed, value) {
		return value
	}
	return "unknown"
}

func (m *DomainMetrics) OnPublicationGate(category string, wait, body, unlock time.Duration) {
	if m == nil || m.publication == nil {
		return
	}
	category = publicationLabel(category, publicationCategories)
	m.publication.gate.WithLabelValues(category, "wait").Observe(wait.Seconds())
	m.publication.gate.WithLabelValues(category, "body").Observe(body.Seconds())
	m.publication.gate.WithLabelValues(category, "unlock").Observe(unlock.Seconds())
}

func (m *DomainMetrics) OnPublicationRead(attempt int, outcome string, capture time.Duration) {
	if m == nil || m.publication == nil {
		return
	}
	label := "unknown"
	if attempt >= 1 && attempt <= 4 {
		label = []string{"1", "2", "3", "fallback"}[attempt-1]
	}
	outcome = publicationLabel(outcome, publicationReadOutcomes)
	m.publication.read.WithLabelValues(label, outcome).Observe(capture.Seconds())
}

func (m *DomainMetrics) OnPublicationApply(delivery, outcome string, preflight time.Duration, acquired bool, committed uint64, pending int) {
	if m == nil || m.publication == nil {
		return
	}
	delivery = publicationLabel(delivery, publicationDeliveries)
	outcome = publicationLabel(outcome, publicationApplyOutcomes)
	stage := "preflight"
	if !acquired && delivery == "committed_duplicate" && outcome == "committed_duplicate" {
		stage = "read_only"
	}
	if acquired {
		stage = "acquired"
		m.publication.committed.Observe(float64(committed))
		m.publication.pending.Observe(float64(pending))
	}
	m.publication.apply.WithLabelValues(delivery, outcome, stage).Inc()
	m.publication.preflight.WithLabelValues(delivery, outcome).Observe(preflight.Seconds())
}
