// Package operationalpolicy defines the closed, environment-independent values
// accepted by the operational management authority. Runtime ownership and local
// dependency checks belong to each group's installed owner.
package operationalpolicy

import (
	"errors"
	"math"
	"time"
)

// Group identifies an atomic operational policy. It is not a configuration key.
type Group string

const (
	GroupSlowRPC     Group = "slow_rpc"
	GroupRate        Group = "rate"
	GroupRequest     Group = "request"
	GroupBackup      Group = "backup"
	GroupCapacity    Group = "capacity"
	GroupLifecycle   Group = "lifecycle"
	GroupSearch      Group = "search"
	GroupReplication Group = "replication"
)

var ErrInvalid = errors.New("invalid operational policy")

// Groups returns the closed, stable ordering of supported policy groups.
func Groups() []Group {
	return []Group{GroupSlowRPC, GroupRate, GroupRequest, GroupBackup, GroupCapacity, GroupLifecycle, GroupSearch, GroupReplication}
}

func (g Group) Valid() bool {
	switch g {
	case GroupSlowRPC, GroupRate, GroupRequest, GroupBackup, GroupCapacity, GroupLifecycle, GroupSearch, GroupReplication:
		return true
	default:
		return false
	}
}

type SlowRPC struct {
	ThresholdMS int64 `json:"threshold_ms"`
}

type Rate struct {
	RPS   float64 `json:"rps"`
	Burst int64   `json:"burst"`
}

type Request struct {
	DeleteByPrefixDefaultLimit int64 `json:"delete_by_prefix_default_limit"`
	DeleteByPrefixMaxLimit     int64 `json:"delete_by_prefix_max_limit"`
	IlluminateMaxK             int64 `json:"illuminate_max_k"`
	IlluminateMaxStep          int64 `json:"illuminate_max_step"`
	MaxBatchSize               int64 `json:"max_batch_size"`
	MaxKeyLen                  int64 `json:"max_key_len"`
	ScanDefaultLimit           int64 `json:"scan_default_limit"`
	ScanMaxLimit               int64 `json:"scan_max_limit"`
	TraversalMaxPushes         int64 `json:"traversal_max_pushes"`
	TraversalMaxResults        int64 `json:"traversal_max_results"`
	TraversalMaxTouchedEdges   int64 `json:"traversal_max_touched_edges"`
	TraversalTimeoutMS         int64 `json:"traversal_timeout_ms"`
}

type Backup struct {
	Enabled       bool  `json:"enabled"`
	IntervalNanos int64 `json:"interval_nanos"`
	Retain        int64 `json:"retain"`
}

type Capacity struct {
	GCEdgeBudget           int64 `json:"gc_edge_budget"`
	GCIntervalSeconds      int64 `json:"gc_interval_seconds"`
	MaxEdges               int64 `json:"max_edges"`
	MaxEdgeCausalEntries   int64 `json:"max_edge_causal_entries"`
	MaxVertexCausalEntries int64 `json:"max_vertex_causal_entries"`
	MaxVertices            int64 `json:"max_vertices"`
}

type Lifecycle struct {
	DrainDelaySeconds      int64  `json:"drain_delay_seconds"`
	MaxReplicationLag      uint64 `json:"max_replication_lag"`
	ShutdownTimeoutSeconds int64  `json:"shutdown_timeout_seconds"`
}

type MatchMode string

const (
	MatchAny       MatchMode = "any"
	MatchAll       MatchMode = "all"
	MatchMinShould MatchMode = "min-should"
)

type Search struct {
	DefaultLimit        int64     `json:"default_limit"`
	MaxLimit            int64     `json:"max_limit"`
	DefaultMode         MatchMode `json:"default_mode"`
	DefaultMinShould    int64     `json:"default_min_should"`
	TimeoutMS           int64     `json:"timeout_ms"`
	MaxQueryBytes       int64     `json:"max_query_bytes"`
	MaxQueryTerms       int64     `json:"max_query_terms"`
	MaxDictionaryVisits int64     `json:"max_dictionary_visits"`
	MaxPostingVisits    int64     `json:"max_posting_visits"`
	MaxPositionVisits   int64     `json:"max_position_visits"`
	MaxExpirationVisits int64     `json:"max_expiration_visits"`
}

type Replication struct {
	AntiEntropyGapWarnThreshold   int64 `json:"anti_entropy_gap_warn_threshold"`
	AntiEntropyIntervalMS         int64 `json:"anti_entropy_interval_ms"`
	AntiEntropySubscribeTimeoutMS int64 `json:"anti_entropy_subscribe_timeout_ms"`
	PumpBackoffMaxMS              int64 `json:"pump_backoff_max_ms"`
	PumpBackoffMinMS              int64 `json:"pump_backoff_min_ms"`
}

// Values is a closed union: exactly one arm must be present. Its fields contain
// only the 43 selected settings; fingerprints and review metadata are separate.
type Values struct {
	SlowRPC     *SlowRPC     `json:"slow_rpc,omitempty"`
	Rate        *Rate        `json:"rate,omitempty"`
	Request     *Request     `json:"request,omitempty"`
	Backup      *Backup      `json:"backup,omitempty"`
	Capacity    *Capacity    `json:"capacity,omitempty"`
	Lifecycle   *Lifecycle   `json:"lifecycle,omitempty"`
	Search      *Search      `json:"search,omitempty"`
	Replication *Replication `json:"replication,omitempty"`
}

func (v Values) Group() (Group, error) {
	groups := []struct {
		group   Group
		present bool
	}{
		{GroupSlowRPC, v.SlowRPC != nil}, {GroupRate, v.Rate != nil},
		{GroupRequest, v.Request != nil}, {GroupBackup, v.Backup != nil},
		{GroupCapacity, v.Capacity != nil}, {GroupLifecycle, v.Lifecycle != nil},
		{GroupSearch, v.Search != nil}, {GroupReplication, v.Replication != nil},
	}
	var selected Group
	for _, candidate := range groups {
		if candidate.present {
			if selected != "" {
				return "", ErrInvalid
			}
			selected = candidate.group
		}
	}
	if selected == "" {
		return "", ErrInvalid
	}
	return selected, nil
}

func (v Values) Validate() error {
	group, err := v.Group()
	if err != nil {
		return err
	}
	valid := false
	switch group {
	case GroupSlowRPC:
		valid = duration(v.SlowRPC.ThresholdMS, time.Millisecond, true)
	case GroupRate:
		r := v.Rate
		valid = !math.IsNaN(r.RPS) && !math.IsInf(r.RPS, 0) && r.RPS >= 0 && !math.Signbit(r.RPS) &&
			(r.RPS == 0 && r.Burst == 0 || r.RPS > 0 && r.Burst > 0)
	case GroupRequest:
		r := v.Request
		valid = positivePair(r.DeleteByPrefixDefaultLimit, r.DeleteByPrefixMaxLimit) &&
			positivePair(r.ScanDefaultLimit, r.ScanMaxLimit) && r.IlluminateMaxK >= 0 && r.IlluminateMaxStep >= 0 &&
			r.MaxBatchSize >= 0 && r.MaxKeyLen >= 0 && r.TraversalMaxPushes > 0 && r.TraversalMaxResults > 0 &&
			r.TraversalMaxTouchedEdges > 0 && duration(r.TraversalTimeoutMS, time.Millisecond, true)
	case GroupBackup:
		valid = v.Backup.IntervalNanos > 0 && v.Backup.Retain >= 0
	case GroupCapacity:
		c := v.Capacity
		valid = c.GCEdgeBudget >= 0 && duration(c.GCIntervalSeconds, time.Second, false) && c.MaxEdges >= 0 &&
			c.MaxEdgeCausalEntries >= 0 && c.MaxVertexCausalEntries >= 0 && c.MaxVertices >= 0
	case GroupLifecycle:
		valid = duration(v.Lifecycle.DrainDelaySeconds, time.Second, true) && duration(v.Lifecycle.ShutdownTimeoutSeconds, time.Second, true)
	case GroupSearch:
		s := v.Search
		valid = positivePair(s.DefaultLimit, s.MaxLimit) && s.DefaultMinShould > 0 && s.DefaultMinShould <= math.MaxUint32 &&
			(s.DefaultMode == MatchAny || s.DefaultMode == MatchAll || s.DefaultMode == MatchMinShould) &&
			duration(s.TimeoutMS, time.Millisecond, false) && s.MaxQueryBytes > 0 && s.MaxQueryTerms > 0 && s.MaxQueryTerms <= s.MaxQueryBytes &&
			s.MaxDictionaryVisits > 0 && s.MaxPostingVisits > 0 && s.MaxPositionVisits > 0 && s.MaxExpirationVisits > 0
	case GroupReplication:
		r := v.Replication
		valid = r.AntiEntropyGapWarnThreshold >= 0 && duration(r.AntiEntropyIntervalMS, time.Millisecond, true) &&
			duration(r.AntiEntropySubscribeTimeoutMS, time.Millisecond, false) && duration(r.PumpBackoffMinMS, time.Millisecond, false) &&
			duration(r.PumpBackoffMaxMS, time.Millisecond, false) && r.PumpBackoffMinMS <= r.PumpBackoffMaxMS
	}
	if !valid {
		return ErrInvalid
	}
	return nil
}

func positivePair(def, max int64) bool {
	return def > 0 && def <= max && max <= math.MaxUint32
}

func duration(value int64, unit time.Duration, zero bool) bool {
	return value >= 0 && (zero || value > 0) && value <= math.MaxInt64/int64(unit)
}

// Clone detaches every arm, including invalid multiple-arm inputs, so validation
// cannot accidentally repair or conceal malformed input.
func (v Values) Clone() Values {
	v.SlowRPC = clone(v.SlowRPC)
	v.Rate = clone(v.Rate)
	v.Request = clone(v.Request)
	v.Backup = clone(v.Backup)
	v.Capacity = clone(v.Capacity)
	v.Lifecycle = clone(v.Lifecycle)
	v.Search = clone(v.Search)
	v.Replication = clone(v.Replication)
	return v
}

func clone[T any](value *T) *T {
	if value == nil {
		return nil
	}
	detached := *value
	return &detached
}

// Defaults returns unadopted values. It never invents a managed generation or a
// Search compatibility fingerprint; the actual owner must resolve that digest.
func Defaults(group Group) (Values, error) {
	switch group {
	case GroupSlowRPC:
		return Values{SlowRPC: &SlowRPC{ThresholdMS: 500}}, nil
	case GroupRate:
		return Values{Rate: &Rate{}}, nil
	case GroupRequest:
		return Values{Request: &Request{DeleteByPrefixDefaultLimit: 10000, DeleteByPrefixMaxLimit: 100000, IlluminateMaxK: 1024,
			IlluminateMaxStep: 16, MaxBatchSize: 10000, MaxKeyLen: 1024, ScanDefaultLimit: 1000, ScanMaxLimit: 10000,
			TraversalMaxPushes: 1000000, TraversalMaxResults: 1024, TraversalMaxTouchedEdges: 10000000, TraversalTimeoutMS: 5000}}, nil
	case GroupBackup:
		return Values{Backup: &Backup{IntervalNanos: int64(5 * time.Minute), Retain: 3}}, nil
	case GroupCapacity:
		return Values{Capacity: &Capacity{GCIntervalSeconds: 60}}, nil
	case GroupLifecycle:
		return Values{Lifecycle: &Lifecycle{MaxReplicationLag: 10000, ShutdownTimeoutSeconds: 30}}, nil
	case GroupSearch:
		return Values{Search: &Search{DefaultLimit: 100, MaxLimit: 1000, DefaultMode: MatchAny, DefaultMinShould: 1, TimeoutMS: 5000,
			MaxQueryBytes: 16384, MaxQueryTerms: 1024, MaxDictionaryVisits: 1000000, MaxPostingVisits: 10000000,
			MaxPositionVisits: 10000000, MaxExpirationVisits: 100000}}, nil
	case GroupReplication:
		return Values{Replication: &Replication{AntiEntropyGapWarnThreshold: 1024, AntiEntropyIntervalMS: 30000,
			AntiEntropySubscribeTimeoutMS: 30000, PumpBackoffMaxMS: 30000, PumpBackoffMinMS: 250}}, nil
	default:
		return Values{}, ErrInvalid
	}
}
