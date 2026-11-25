package backup

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics holds the Prometheus collectors for the backup engine. They are
// registered on the shared server registry when one is provided; with a nil
// registerer the gauges still function (Set/Inc are no-ops observability-
// wise) so the Backupper never has to nil-check.
type metrics struct {
	lastSuccess prometheus.Gauge
	duration    prometheus.Gauge
	setMembers  prometheus.Gauge
	setBytes    prometheus.Gauge
	failures    prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful private backup.",
		}),
		duration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_duration_seconds",
			Help: "Wall-clock seconds the last successful backup took.",
		}),
		setMembers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_set_members",
			Help: "Members in the last verified physical backup point.",
		}),
		setBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_set_bytes",
			Help: "Total bytes in the last verified physical backup point, including its application cut.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_backup_failures_total",
			Help: "Total failed private backup attempts.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.lastSuccess, m.duration, m.setMembers, m.setBytes, m.failures)
	}
	return m
}

func (m *metrics) observeBackup(s Stats, took time.Duration, at time.Time) {
	m.lastSuccess.Set(float64(at.Unix()))
	m.duration.Set(took.Seconds())
	m.setMembers.Set(float64(s.Members))
	m.setBytes.Set(float64(s.Bytes))
}
