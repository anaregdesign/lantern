package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/anaregdesign/lantern/server/internal/readinessdiag"
)

type fixtureObservation struct {
	Status string                  `json:"status"`
	AgeMS  int64                   `json:"age_ms"`
	State  *readinessdiag.Snapshot `json:"state,omitempty"`
}

// The age is time since the fixture first read this sequence, not a shared
// authority-clock timestamp. State contains separately sampled worker/Health
// facts. Missing, stale or invalid observation never certifies readiness.
type fixtureObservationReader struct {
	state    *readinessdiag.Snapshot
	seen     time.Time
	advanced bool
}

func (r *fixtureObservationReader) read(path string, now time.Time) fixtureObservation {
	result := fixtureObservation{Status: "not_observed"}
	file, err := os.Open(path)
	if err != nil {
		result.Status = "unavailable"
		return result
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		result.Status = "unavailable"
		return result
	}
	if len(raw) > 64<<10 {
		result.Status = "truncated"
		return result
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		result.Status = "incomplete"
		return result
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	var latest *readinessdiag.Snapshot
	for scanner.Scan() {
		var header struct {
			Message string `json:"msg"`
		}
		if json.Unmarshal(scanner.Bytes(), &header) != nil || header.Message != readinessdiag.Message {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		var record readinessdiag.Record
		if len(scanner.Bytes())+1 > readinessdiag.MaxRecordBytes || decoder.Decode(&record) != nil || !record.State.Valid() || latest != nil && record.State.Sequence <= latest.Sequence {
			result.Status = "invalid"
			return result
		}
		state := record.State
		latest = &state
	}
	if scanner.Err() != nil {
		result.Status = "truncated"
		return result
	}
	if latest == nil {
		return result
	}
	if r.state != nil && latest.Sequence < r.state.Sequence {
		result.Status = "invalid"
		return result
	}
	if r.state == nil || latest.Sequence != r.state.Sequence {
		r.advanced = r.state != nil
		r.state, r.seen = latest, now
	}
	result.State = latest
	result.AgeMS = max(0, now.Sub(r.seen).Milliseconds())
	result.Status = "observed"
	if !r.advanced {
		result.Status = "first_observed"
	}
	if now.Sub(r.seen) > readinessdiag.StaleAfter {
		result.Status = "stale"
	}
	if latest.Exhausted {
		result.Status = "exhausted"
	}
	return result
}

func fixtureHealthCategory(status string) string {
	switch status {
	case "":
		return "missing"
	case "SERVING_STATUS_SERVING":
		return "serving"
	case "SERVING_STATUS_NOT_SERVING":
		return "not_serving"
	case "SERVING_STATUS_UNKNOWN":
		return "unknown"
	case "SERVING_STATUS_SERVICE_UNKNOWN":
		return "service_unknown"
	default:
		return "unrecognized"
	}
}
