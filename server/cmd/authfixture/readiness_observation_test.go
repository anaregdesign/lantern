package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/readinessdiag"
)

func TestFixturePassiveObservationBoundsAndFreshness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.log")
	state := readinessdiag.Snapshot{Version: 1, Sequence: 1, WorkerStarted: 4, WorkerDone: 3, WorkerPublish: 3, WorkerState: "in_progress", WorkerReason: "clock_fault", HealthCount: 9, HealthBranch: "static_rejection", HealthReason: "not_evaluated"}
	raw, _ := json.Marshal(readinessdiag.Record{Message: readinessdiag.Message, State: state})
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var reader fixtureObservationReader
	now := time.Now()
	if got := reader.read(path, now); got.Status != "first_observed" || got.State.WorkerReason != "clock_fault" {
		t.Fatal(got)
	}
	state.Sequence++
	next, _ := json.Marshal(readinessdiag.Record{Message: readinessdiag.Message, State: state})
	if err := os.WriteFile(path, append(append(raw, next...), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if got := reader.read(path, now.Add(time.Second)); got.Status != "observed" || got.AgeMS != 0 {
		t.Fatal(got)
	}
	if got := reader.read(path, now.Add(time.Second+readinessdiag.StaleAfter+time.Millisecond)); got.Status != "stale" || got.AgeMS != readinessdiag.StaleAfter.Milliseconds()+1 {
		t.Fatal(got)
	}
	for _, tc := range []struct{ name, raw, want string }{
		{"missing", "", "not_observed"},
		{"truncated", strings.Repeat("x", 64<<10) + "\n", "truncated"},
		{"incomplete", string(bytes.TrimSpace(raw)), "incomplete"},
		{"unknown version", strings.Replace(string(raw), `"version":1`, `"version":2`, 1), "invalid"},
		{"unknown category", strings.Replace(string(raw), "clock_fault", "private-token-key-body", 1), "invalid"},
		{"extra field", strings.Replace(string(raw), `"version":1`, `"version":1,"private":"token-key-body"`, 1), "invalid"},
		{"exhausted", strings.Replace(strings.Replace(string(raw), `"sequence":1`, `"sequence":64`, 1), `"exhausted":false`, `"exhausted":true`, 1), "exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			var r fixtureObservationReader
			got := r.read(path, now)
			retained, _ := json.Marshal(got)
			if got.Status != tc.want || strings.Contains(string(retained), "private-token") || strings.Contains(string(retained), "token-key-body") {
				t.Fatal(got)
			}
		})
	}
}

func TestFixtureFailureRetainsPassiveRefusalWithoutAnotherCapabilitiesCall(t *testing.T) {
	var capabilities atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/grpc.health.v1.Health/Check" {
			capabilities.Add(1)
		}
		_, _ = w.Write([]byte(`{"status":"SERVING_STATUS_NOT_SERVING"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "private.log"))
	if err != nil {
		t.Fatal(err)
	}
	observer := readinessdiag.New(log)
	observer.WorkerStart()
	observer.WorkerDone("clock_fault")
	observer.WorkerPublished()
	observer.Health("static_rejection", "not_evaluated")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = waitFixtureReady(ctx, fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}, []*fixtureProcess{{done: make(chan struct{}), log: log}})
	_ = log.Close()
	if removeErr := os.RemoveAll(dir); removeErr != nil {
		t.Fatal(removeErr)
	}
	var output bytes.Buffer
	writeFixtureFailure(&output, &fixtureFailure{stage: "readiness", cause: err})
	if err == nil || capabilities.Load() != 0 || output.Len() >= 4096 || strings.Contains(output.String(), dir) {
		t.Fatal("missing failure, extra evaluation, or unbounded/private output")
	}
	for _, part := range []string{`"last_health":"not_serving"`, `"last_capability":"not_probed"`, `"capability_probes":0`, `"health_probes":1`, `"worker_reason":"clock_fault"`, `"health_reason":"not_evaluated"`, `"worker_publish":1`} {
		if !strings.Contains(output.String(), part) {
			t.Fatal("lost retained diagnostic", part, output.String())
		}
	}
}
