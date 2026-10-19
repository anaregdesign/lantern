package decisiontrace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// ContextState is a fixed diagnostic classification, never a time input.
type ContextState string

const (
	ContextUnobserved       ContextState = "unobserved"
	ContextLive             ContextState = "live"
	ContextCanceled         ContextState = "canceled"
	ContextDeadlineExceeded ContextState = "deadline_exceeded"
	ContextOtherError       ContextState = "other_error"
)

// StartupAttempt contains only copied classifications from one completed
// exchange. RemainingNS is observed at Read entry, before cleanup cancellation.
// It contains no endpoint, packet, identity, UTC or native counter sample.
type StartupAttempt struct {
	Attempt         uint64       `json:"attempt"`
	Stage           string       `json:"stage"`
	Reason          string       `json:"reason"`
	HasReadBudget   bool         `json:"has_read_budget"`
	ReadRemainingNS int64        `json:"read_remaining_ns"`
	ExchangeContext ContextState `json:"exchange_context"`
	IncomingContext ContextState `json:"incoming_context"`
	ReadCount       uint8        `json:"read_count"`
	FirstReadError  string       `json:"first_read_error,omitempty"`
	FinalReadError  string       `json:"final_read_error,omitempty"`
}

// StartupSnapshot is a fixed-size value returned with a failed constructor.
// Attempts are accepted owner observations in chronological order. Producer
// state is separate: a completed but unpublished or in-flight exchange must
// not inherit the last accepted attempt's error or classification.
type StartupSnapshot struct {
	Attempts         [4]StartupAttempt `json:"attempts"`
	Count            uint8             `json:"count"`
	Overflow         bool              `json:"overflow"`
	PublishedAttempt uint64            `json:"published_attempt"`
	StartedAttempt   uint64            `json:"started_attempt"`
	CompletedAttempt uint64            `json:"completed_attempt"`
	InFlight         bool              `json:"in_flight"`
	OwnerFailed      bool              `json:"owner_failed"`
	OwnerClosed      bool              `json:"owner_closed"`
	RemainingNS      int64             `json:"remaining_ns"`
	AnchoringContext ContextState      `json:"anchoring_context"`
	IncomingContext  ContextState      `json:"incoming_context"`
	OwnerContext     ContextState      `json:"owner_context"`
}

const StartupMessage = "authority_time_startup"
const MaxStartupRecordBytes = 4096

// StartupRecord carries only the failed constructor's copied diagnostic. It is
// never a readiness certificate, source measurement or retry-policy input.
type StartupRecord struct {
	Message string          `json:"msg"`
	Version uint8           `json:"version"`
	State   StartupSnapshot `json:"state"`
}

func startupContextValid(state ContextState) bool {
	switch state {
	case ContextUnobserved, ContextLive, ContextCanceled, ContextDeadlineExceeded, ContextOtherError:
		return true
	}
	return false
}

func startupLabelValid(label string) bool {
	switch label {
	case "dns", "random", "dial", "native_send", "send", "read", "native_receive", "packet", "native_binding", "measured",
		"binding", "rtt", "source_bound", "transmit", "receive", "reference", "overflow", "interval", "native_install",
		"elapsed", "age", "anchor_interval", "utc_window", "width", "contradiction", "installed",
		"ok", "read_timeout", "rate", "deny", "source_fault", "install_rejected", "native_terminal":
		return true
	}
	return false
}

func startupReadErrorValid(label string) bool {
	switch label {
	case "none", "aborted", "timeout", "canceled", "closed", "other":
		return true
	}
	return false
}

// Valid rejects unknown text rather than forwarding it across process boundaries.
func (s StartupSnapshot) Valid() bool {
	if int(s.Count) > len(s.Attempts) || s.PublishedAttempt > s.CompletedAttempt || s.CompletedAttempt > s.StartedAttempt || s.RemainingNS < 0 || s.RemainingNS > 45_000_000_000 || !startupContextValid(s.AnchoringContext) || !startupContextValid(s.IncomingContext) || !startupContextValid(s.OwnerContext) {
		return false
	}
	var previous uint64
	for i, a := range s.Attempts {
		if i >= int(s.Count) {
			if a != (StartupAttempt{}) {
				return false
			}
			continue
		}
		if a.Attempt <= previous || !startupLabelValid(a.Stage) || !startupLabelValid(a.Reason) || !startupContextValid(a.ExchangeContext) || !startupContextValid(a.IncomingContext) || a.ReadCount > 2 || a.ReadRemainingNS > 3_000_000_000 {
			return false
		}
		if a.ReadCount == 0 {
			if a.FirstReadError != "" || a.FinalReadError != "" {
				return false
			}
		} else if !a.HasReadBudget || !startupReadErrorValid(a.FirstReadError) || !startupReadErrorValid(a.FinalReadError) || a.ReadCount == 1 && a.FirstReadError != a.FinalReadError || a.ReadCount == 2 && a.FirstReadError != "aborted" {
			return false
		}
		previous = a.Attempt
	}
	return previous == s.PublishedAttempt
}

// WriteStartupFailure emits one bounded record, without consulting any native
// clock or retaining error text. An unavailable/invalid snapshot emits nothing.
func WriteStartupFailure(w io.Writer, err error) {
	var source interface{ AuthorityTimeStartupSnapshot() StartupSnapshot }
	if !errors.As(err, &source) {
		return
	}
	state := source.AuthorityTimeStartupSnapshot()
	if !state.Valid() {
		return
	}
	raw, marshalErr := json.Marshal(StartupRecord{Message: StartupMessage, Version: 1, State: state})
	if marshalErr == nil && len(raw)+1 <= MaxStartupRecordBytes {
		_, _ = w.Write(append(raw, '\n'))
	}
}

// DecodeStartupRecord refuses extra fields, trailing data and unbounded records.
func DecodeStartupRecord(raw []byte) (StartupSnapshot, bool) {
	if len(raw)+1 > MaxStartupRecordBytes {
		return StartupSnapshot{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record StartupRecord
	if decoder.Decode(&record) != nil || record.Message != StartupMessage || record.Version != 1 || !record.State.Valid() {
		return StartupSnapshot{}, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return StartupSnapshot{}, false
	}
	return record.State, true
}
