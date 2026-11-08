package replication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func TestSnapshotInstallDiagnosticPreservesErrorAndBoundsLocalFields(t *testing.T) {
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	cause := connect.NewError(connect.CodeResourceExhausted, errors.New("controlled staging rejection"))
	diagnostic := newSnapshotInstallDiagnostic(start)
	diagnostic.phase, diagnostic.phaseStarted = "seal", start.Add(time.Second)
	diagnostic.stagedFrames, diagnostic.stagedBytes = 3, 1234
	admission := peerauth.AdmissionDiagnostics{Reason: "native_lifetime_expired", RemainingKnown: true}
	err := diagnostic.failure(cause, start.Add(2*time.Second), admission)
	var status *connect.Error
	var failure *snapshotInstallError
	if err.Error() != cause.Error() || !errors.Is(err, cause) || !errors.As(err, &status) || status != cause || connect.CodeOf(err) != connect.CodeResourceExhausted || !errors.As(err, &failure) {
		t.Fatalf("diagnostic obscured original cause/status: %v", err)
	}
	if failure.diagnostic.elapsed != 2*time.Second || failure.diagnostic.phaseElapsed != time.Second {
		t.Fatalf("unexpected diagnostic timings: %+v", failure.diagnostic)
	}
	diagnostic.phase, diagnostic.stagedFrames = "changed", 99
	if failure.diagnostic.phase != "seal" || failure.diagnostic.stagedFrames != 3 {
		t.Fatal("returned failure retained mutable attempt state")
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logger.Warn("existing failure", snapshotFailureAttrs(err)...)
	for _, expected := range []string{`"snapshot_phase":"seal"`, `"snapshot_live_started":false`, `"snapshot_staged_frames":3`, `"snapshot_staged_bytes":1234`, `"snapshot_last_admission_check":"native_lifetime_expired"`, `"snapshot_last_admission_original_remaining":0`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing fixed diagnostic %s: %s", expected, &output)
		}
	}
	if strings.Contains(output.String(), cause.Error()) || len(snapshotFailureAttrs(cause)) != 0 || diagnostic.failure(nil, start, admission) != nil {
		t.Fatal("diagnostic attributes duplicated cause or invented a failure")
	}
	if !strings.Contains(output.String(), `"snapshot_failure_return_observed":false`) || strings.Contains(output.String(), `"snapshot_deferred_unwind_elapsed"`) {
		t.Fatal("diagnostic invented a return observation before setup completed")
	}
}

func TestSnapshotInstallDiagnosticSeparatesFailureReturnFromDeferredUnwind(t *testing.T) {
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	primary := connect.NewError(connect.CodeDeadlineExceeded, context.DeadlineExceeded)
	cleanup := errors.New("controlled deferred cleanup error")
	cause := errors.Join(primary, cleanup)
	diagnostic := newSnapshotInstallDiagnostic(start)
	diagnostic.phase, diagnostic.phaseStarted = "seal", start.Add(time.Second)
	diagnostic.sealReadAttempted, diagnostic.sealReadBytes = true, 123
	diagnostic.observeFailureReturn(start.Add(5 * time.Second))
	diagnostic.observeFailureReturn(start.Add(6 * time.Second))
	err := diagnostic.failure(cause, start.Add(8*time.Second), peerauth.AdmissionDiagnostics{})
	var failure *snapshotInstallError
	if !errors.As(err, &failure) || !errors.Is(err, primary) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cleanup) ||
		err.Error() != cause.Error() || connect.CodeOf(err) != connect.CodeDeadlineExceeded {
		t.Fatalf("return observation obscured original joined errors/status: %v", err)
	}
	detail := failure.diagnostic
	if detail.elapsed != 8*time.Second || detail.phaseElapsed != 7*time.Second ||
		detail.failureReturnElapsed != 5*time.Second || detail.failureReturnPhaseElapsed != 4*time.Second || detail.deferredUnwindElapsed != 3*time.Second {
		t.Fatalf("operation return and deferred unwind were conflated: %+v", detail)
	}
	diagnostic.failureReturnedAt, diagnostic.sealReadBytes = start, 999
	if failure.diagnostic.failureReturnedAt != start.Add(5*time.Second) || failure.diagnostic.sealReadBytes != 123 {
		t.Fatal("returned diagnostic retained mutable operation/progress state")
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Warn("existing failure", snapshotFailureAttrs(err)...)
	for _, expected := range []string{
		`"snapshot_elapsed":8000000000`, `"snapshot_phase_elapsed":7000000000`,
		`"snapshot_failure_return_observed":true`, `"snapshot_failure_return_elapsed":5000000000`,
		`"snapshot_failure_return_phase_elapsed":4000000000`, `"snapshot_deferred_unwind_elapsed":3000000000`,
		`"snapshot_seal_read_attempted":true`, `"snapshot_seal_read_bytes":123`, `"snapshot_seal_read_eof":false`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing bounded failure observation %s: %s", expected, &output)
		}
	}
}
