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
	lastSuccess   prometheus.Gauge
	duration      prometheus.Gauge
	vertices      prometheus.Gauge
	edges         prometheus.Gauge
	failures      prometheus.Counter
	restoreVtx    prometheus.Gauge
	restoreEdges  prometheus.Gauge
	restoreLastTS prometheus.Gauge
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful periodic backup.",
		}),
		duration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_duration_seconds",
			Help: "Wall-clock seconds the last successful backup took.",
		}),
		vertices: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_vertices",
			Help: "Vertices written by the last successful backup.",
		}),
		edges: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_backup_edges",
			Help: "Edges written by the last successful backup.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_backup_failures_total",
			Help: "Total periodic backups that failed to write.",
		}),
		restoreVtx: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_restore_vertices",
			Help: "Vertices replayed by restore-on-startup.",
		}),
		restoreEdges: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_restore_edges",
			Help: "Edges replayed by restore-on-startup.",
		}),
		restoreLastTS: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_restore_last_timestamp_seconds",
			Help: "Unix timestamp of the last restore-on-startup.",
		}),
	}
	if reg != nil {
		reg.MustRegister(
			m.lastSuccess, m.duration, m.vertices, m.edges, m.failures,
			m.restoreVtx, m.restoreEdges, m.restoreLastTS,
		)
	}
	return m
}

func (m *metrics) observeBackup(s Stats, took time.Duration, at time.Time) {
	m.lastSuccess.Set(float64(at.Unix()))
	m.duration.Set(took.Seconds())
	m.vertices.Set(float64(s.Vertices))
	m.edges.Set(float64(s.Edges))
}

func (m *metrics) observeRestore(s Stats, at time.Time) {
	m.restoreVtx.Set(float64(s.Vertices))
	m.restoreEdges.Set(float64(s.Edges))
	m.restoreLastTS.Set(float64(at.Unix()))
}
