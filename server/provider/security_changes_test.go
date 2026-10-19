package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type changeDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *changeDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestSecurityChangesRequireTransportWriteDeadline(t *testing.T) {
	runtime := &SecurityRuntime{mode: "off", now: time.Now}
	req := httptest.NewRequest(http.MethodPost, "/graph.v1.LanternChangeService/WatchChanges", nil)
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if deadline, exists := req.Context().Deadline(); !exists || time.Until(deadline) > 28*time.Second {
			t.Fatal("handler lacks bounded stream lifetime")
		}
		w.WriteHeader(http.StatusOK)
	})
	w := &changeDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	runtime.serveBoundedChanges(w, req, handler)
	if !called || len(w.deadlines) != 2 || w.deadlines[0].IsZero() || !w.deadlines[1].IsZero() {
		t.Fatal("transport deadline not installed/reset", w.deadlines)
	}
	called = false
	unsupported := httptest.NewRecorder()
	runtime.serveBoundedChanges(unsupported, req, handler)
	if called || unsupported.Code != http.StatusServiceUnavailable {
		t.Fatal("unsupported transport silently enabled an unbounded stream")
	}
	runtime.mode = "oidc"
	w = &changeDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	runtime.serveBoundedChanges(w, req, handler)
	if called || len(w.deadlines) != 0 || w.Code != http.StatusServiceUnavailable {
		t.Fatal("OIDC stream deadline fabricated admission")
	}
}

func TestSecurityChangesReserveTerminalTimeWithinOffWindow(t *testing.T) {
	runtime := &SecurityRuntime{mode: "off", now: time.Now}
	req := httptest.NewRequest(http.MethodPost, "/changes", nil)
	writer := &changeDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	started := time.Now()
	runtime.serveBoundedChanges(writer, req, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		deadline, exists := req.Context().Deadline()
		if !exists || len(writer.deadlines) != 1 {
			t.Fatal("missing bounded OFF transport/context")
		}
		hard := writer.deadlines[0]
		if hard.Sub(started) > 28*time.Second+10*time.Millisecond || hard.Sub(deadline) != 100*time.Millisecond {
			t.Fatal("OFF window extended or terminal budget lost")
		}
	}))
	if len(writer.deadlines) != 2 || !writer.deadlines[1].IsZero() {
		t.Fatal("transport deadline not reset")
	}
}
