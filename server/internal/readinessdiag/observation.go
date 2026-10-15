// Package readinessdiag retains passive, content-free native fixture evidence.
// It never evaluates readiness or reads an authority clock.
package readinessdiag

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

const Message = "native fixture readiness observation"
const MaxRecords = 64
const MaxRecordBytes = 1024
const Interval = 5 * time.Second
const StaleAfter = 2 * Interval

// Snapshot correlates separate observations, not an atomic security/gate cut.
// Worker completion and publication refer to the same numbered worker attempt.
type Snapshot struct {
	Version       uint8  `json:"version"`
	Sequence      uint64 `json:"sequence"`
	WorkerStarted uint64 `json:"worker_started"`
	WorkerDone    uint64 `json:"worker_done"`
	WorkerPublish uint64 `json:"worker_publish"`
	WorkerState   string `json:"worker_state"`
	WorkerReason  string `json:"worker_reason"`
	WorkerAgeMS   int64  `json:"worker_age_ms"`
	HealthCount   uint64 `json:"health_count"`
	HealthBranch  string `json:"health_branch"`
	HealthReason  string `json:"health_reason"`
	Exhausted     bool   `json:"exhausted"`
}

func ReasonValid(s string) bool {
	switch s {
	case "not_evaluated", "ready", "restart_wait", "clock_fault", "native_revision_unavailable", "store_revision_refusal", "context", "unavailable":
		return true
	}
	return false
}

func (s Snapshot) Valid() bool {
	if s.Version != 1 || s.Sequence == 0 || s.Sequence > MaxRecords || s.WorkerDone > s.WorkerStarted || s.WorkerPublish > s.WorkerDone || s.WorkerAgeMS < 0 || !ReasonValid(s.WorkerReason) || !ReasonValid(s.HealthReason) {
		return false
	}
	switch s.WorkerState {
	case "never_started", "in_progress", "completed", "published", "stopped_canceled", "stopped":
	default:
		return false
	}
	switch s.HealthBranch {
	case "not_observed", "static_rejection", "runtime_rejection", "serving", "error":
	default:
		return false
	}
	return true
}

type Record struct {
	Message string   `json:"msg"`
	State   Snapshot `json:"readiness"`
}

// Observer is installed once, before the native fixture app starts workers or
// listeners. Other server launches have no observer and do no diagnostic I/O.
type Observer struct {
	mu       sync.Mutex
	outputMu sync.Mutex
	state    Snapshot
	updated  time.Time
	lastEmit time.Time
	now      func() time.Time
	out      io.Writer
}

func New(out io.Writer) *Observer {
	return &Observer{out: out, now: time.Now, state: Snapshot{Version: 1, WorkerState: "never_started", WorkerReason: "not_evaluated", HealthBranch: "not_observed", HealthReason: "not_evaluated"}}
}

// Snapshot only copies observations. It does not inspect security or gate state.
func (o *Observer) Snapshot() Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.state
	if !o.updated.IsZero() {
		s.WorkerAgeMS = max(0, o.now().Sub(o.updated).Milliseconds())
	}
	return s
}

func (o *Observer) WorkerStart() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.WorkerStarted++
	o.state.WorkerState = "in_progress"
	o.updated = o.now()
}

func (o *Observer) WorkerDone(reason string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.WorkerDone = o.state.WorkerStarted
	o.state.WorkerState, o.state.WorkerReason = "completed", reason
	o.updated = o.now()
}

func (o *Observer) WorkerPublished() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.WorkerPublish = o.state.WorkerDone
	o.state.WorkerState = "published"
	o.updated = o.now()
}

func (o *Observer) WorkerStopped(canceled bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.state.WorkerState = "stopped"
	if canceled {
		o.state.WorkerState = "stopped_canceled"
	}
	o.updated = o.now()
}

// Health is called after the actual Health decision, outside authority/gate
// locks. At most 64 bounded records cover the maximum five-minute fixture
// startup. Exhaustion and stale/missing records never imply readiness.
func (o *Observer) Health(branch, reason string) {
	if o == nil {
		return
	}
	o.outputMu.Lock()
	defer o.outputMu.Unlock()
	o.mu.Lock()
	now := o.now()
	o.state.HealthCount++
	o.state.HealthBranch, o.state.HealthReason = branch, reason
	if o.state.Sequence >= MaxRecords || !o.lastEmit.IsZero() && now.Sub(o.lastEmit) < Interval {
		o.mu.Unlock()
		return
	}
	o.state.Sequence++
	o.state.Exhausted = o.state.Sequence == MaxRecords
	o.lastEmit = now
	snapshot := o.state
	if !o.updated.IsZero() {
		snapshot.WorkerAgeMS = max(0, now.Sub(o.updated).Milliseconds())
	}
	o.mu.Unlock()
	if !snapshot.Valid() {
		return
	}
	raw, err := json.Marshal(Record{Message: Message, State: snapshot})
	if err == nil && len(raw)+1 <= MaxRecordBytes {
		_, _ = o.out.Write(append(raw, '\n'))
	}
}
