package membershippublisher

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

var ErrReporting = errors.New("membership publisher: status reporting failed; retain state")

// Status contains only bounded operational observations, never raw errors,
// filenames, workload identities, signed envelopes or authority credentials.
type Status struct {
	Version                uint32         `json:"version"`
	StartedAt              time.Time      `json:"started_at"`
	ObservedAt             time.Time      `json:"observed_at"`
	Failure                string         `json:"failure"`
	End                    time.Time      `json:"end"`
	CleanupBy              time.Time      `json:"cleanup_by"`
	IssuanceSeconds        int64          `json:"issuance_seconds"`
	TLSCapacityEnd         time.Time      `json:"tls_capacity_end"`
	MaintenanceLeadSeconds int64          `json:"maintenance_lead_seconds"`
	MaintenanceWarning     bool           `json:"maintenance_warning"`
	DeliveryWarningSeconds int64          `json:"delivery_warning_seconds"`
	Streams                []StreamStatus `json:"streams"`
	NodeAcceptanceObserved bool           `json:"node_acceptance_observed"`
	WallClockOnly          bool           `json:"wall_clock_diagnostics_only"`
}

type StreamStatus struct {
	Kind           string         `json:"kind"`
	JournalVersion uint64         `json:"journal_version"`
	ExpiresAt      time.Time      `json:"candidate_expires_at"`
	Remaining      int64          `json:"candidate_remaining_seconds"`
	Pending        bool           `json:"pending"`
	NextIssueAt    time.Time      `json:"next_issue_at"`
	Targets        []TargetStatus `json:"targets"`
}

type TargetStatus struct {
	Ordinal            int        `json:"ordinal"`
	ObservedVersion    uint64     `json:"observed_version"`
	ObservedAt         time.Time  `json:"observed_at"`
	ExpiresAt          *time.Time `json:"expires_at"`
	Remaining          *int64     `json:"remaining_seconds"`
	CandidateMatches   bool       `json:"candidate_matches"`
	LastDurableVersion uint64     `json:"last_durable_version"`
	LastDurableAt      *time.Time `json:"last_durable_at"`
	Failure            string     `json:"failure"`
	Warning            bool       `json:"warning"`
	Urgent             bool       `json:"urgent"`
}

// FailureCategory returns a fixed nonsecret operator action category.
func FailureCategory(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrBudget):
		return "budget_exhausted"
	case errors.Is(err, ErrPublication):
		return "delivery_retry"
	case errors.Is(err, ErrCapacity):
		return "trust_capacity"
	case errors.Is(err, ErrJournal):
		return "journal_uncertain"
	case errors.Is(err, ErrReporting):
		return "reporting_failure"
	case errors.Is(err, context.Canceled):
		return "stopped"
	default:
		return "operator_recovery_required"
	}
}

func (p *Publisher) observeTarget(i, j int, raw []byte, failure string) {
	s := &p.targetStatus[i][j]
	s.Ordinal, s.ObservedAt, s.Failure = j+1, p.clock().UTC(), failure
	s.ObservedVersion, s.ExpiresAt = 0, nil
	if raw != nil {
		version, _, end, err := p.verify(i, raw)
		if err == nil {
			s.ObservedVersion, s.ExpiresAt = version, &end
		}
	}
}

// Status takes no I/O or native-time samples and never treats in-memory state
// after an uncertain save as a committed successor. Delivery dates are known
// only after this process completes file AND directory sync; restart leaves
// dates null. Supervisor-retained output provides historical observations.
func (p *Publisher) Status(now time.Time, maintenanceLead time.Duration) Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.config
	warning := 2*c.Interval + 2*peerauth.ClockMargin
	cleanup := c.CleanupBy.Sub(c.End)
	// Compare differences rather than overflowing an addition of durations.
	if maintenanceLead < cleanup || maintenanceLead-cleanup < warning {
		maintenanceLead = cleanup
		if time.Duration(1<<63-1)-maintenanceLead >= warning {
			maintenanceLead += warning
		} else {
			maintenanceLead = time.Duration(1<<63 - 1)
		}
	}
	s := Status{Version: 1, StartedAt: p.started, ObservedAt: now.UTC(), Failure: p.failure,
		End: c.End, CleanupBy: c.CleanupBy, IssuanceSeconds: int64(c.End.Sub(now) / time.Second),
		TLSCapacityEnd: p.capacity, MaintenanceLeadSeconds: int64(maintenanceLead / time.Second),
		DeliveryWarningSeconds: int64(warning / time.Second), WallClockOnly: true}
	if s.Failure == "" {
		s.Failure = "none"
	}
	s.MaintenanceWarning = c.End.Sub(now) <= maintenanceLead || p.capacity.Sub(now) <= maintenanceLead
	var confirmed journal
	if decode(p.raw, &confirmed) != nil {
		s.Failure = "operator_recovery_required"
		return s
	}
	for i, saved := range confirmed.Streams {
		// p.raw was authenticated when retained. Parsing its timestamps does
		// not require keeping signing keys alive after Close.
		var envelope struct {
			Payload struct {
				IssuedAt  time.Time `json:"issued_at"`
				ExpiresAt time.Time `json:"expires_at"`
			}
		}
		if json.Unmarshal(saved.Current, &envelope) != nil {
			s.Failure = "operator_recovery_required"
			return s
		}
		x := StreamStatus{Kind: c.Streams[i].Kind, JournalVersion: saved.Version,
			ExpiresAt: envelope.Payload.ExpiresAt, Remaining: int64(envelope.Payload.ExpiresAt.Sub(now) / time.Second),
			Pending: saved.Pending, NextIssueAt: envelope.Payload.IssuedAt.Add(c.Interval),
			Targets: append([]TargetStatus{}, p.targetStatus[i]...)}
		for j := range x.Targets {
			target := &x.Targets[j]
			if target.ExpiresAt != nil {
				value := *target.ExpiresAt
				target.ExpiresAt = &value
			}
			if target.LastDurableAt != nil {
				value := *target.LastDurableAt
				target.LastDurableAt = &value
			}
			target.CandidateMatches = target.ObservedVersion == saved.Version
			target.Warning = target.Failure != "" || target.ExpiresAt == nil
			if target.ExpiresAt != nil {
				left := target.ExpiresAt.Sub(now)
				seconds := int64(left / time.Second)
				target.Remaining = &seconds
				target.Warning = target.Warning || left <= warning
				target.Urgent = left <= c.Interval+2*peerauth.ClockMargin
			}
		}
		s.Streams = append(s.Streams, x)
	}
	return s
}
