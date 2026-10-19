package integration_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

// This opt-in observation uses the same fresh scoped fixture as the public Q
// case. Capture is passive: constructor deadlines, source selection, retries,
// anchor installation and readiness all retain their production behavior.
func TestCurrentAuthorityConstructorDiagnostic(t *testing.T) {
	if os.Getenv("LANTERN_CURRENT_CONSTRUCTOR_DIAGNOSTIC") != "1" {
		t.Skip("requires an explicitly requested normal constructor diagnostic")
	}
	if os.Getenv("LANTERN_CURRENT_PUBLIC_GATE") != "1" {
		t.Fatal("normal public fixture opt-in is required")
	}
	capture := decisiontrace.Start(512)
	if capture == nil {
		t.Fatal("diagnostic capture is already active")
	}
	started := time.Now()
	defer func() {
		capture.Stop()
		events, dropped := capture.Drain()
		type observation struct {
			Kind      string `json:"kind"`
			Attempt   uint64 `json:"attempt,omitempty"`
			Stage     string `json:"stage,omitempty"`
			Reason    string `json:"reason"`
			BackoffNS int64  `json:"backoff_ns,omitempty"`
			HasAnchor bool   `json:"has_anchor"`
		}
		// Copy only existing classifications and scheduling facts. Native/UTC
		// samples, identities, endpoints and arbitrary errors stay private.
		observations := make([]observation, 0, len(events))
		for _, event := range events {
			if event.Kind != "time_probe" && event.Kind != "time_current" {
				continue
			}
			observations = append(observations, observation{
				Kind: event.Kind, Attempt: event.Attempt, Stage: event.Stage,
				Reason: event.Reason, BackoffNS: event.Delay, HasAnchor: event.HasAnchor,
			})
		}
		result := struct {
			Outcome      string        `json:"outcome"`
			ElapsedMS    int64         `json:"elapsed_ms"`
			Dropped      uint64        `json:"dropped"`
			Observations []observation `json:"observations"`
		}{Outcome: "ready", ElapsedMS: time.Since(started).Milliseconds(), Dropped: dropped, Observations: observations}
		if t.Failed() {
			result.Outcome = "failed"
		}
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("AUTHORITY_CONSTRUCTOR_DIAGNOSTIC " + string(raw))
	}()
	_ = newScopedSDKFixture(t)
}
