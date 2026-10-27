package replication

import (
	"errors"
	"log/slog"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// snapshotInstallDiagnostic is fixed-size attempt-local observation only. It
// cannot admit work, retain frames, renew authority or certify publication.
// Timers are sampled at phase transitions and failure, never per frame.
type snapshotInstallDiagnostic struct {
	phase                                     string
	started, phaseStarted                     time.Time
	elapsed, phaseElapsed                     time.Duration
	liveStarted                               bool
	stagedFrames, stagedBytes, liveFramesRead uint64
	cleanupFailed                             bool
	admission                                 peerauth.AdmissionDiagnostics
}

func newSnapshotInstallDiagnostic(now time.Time) *snapshotInstallDiagnostic {
	return &snapshotInstallDiagnostic{phase: "configuration", started: now, phaseStarted: now}
}

func (d *snapshotInstallDiagnostic) enter(phase string) {
	if d == nil {
		return
	}
	d.phase, d.phaseStarted = phase, time.Now()
}

func (d *snapshotInstallDiagnostic) failure(cause error, now time.Time, admission peerauth.AdmissionDiagnostics) error {
	if cause == nil {
		return nil
	}
	observation := *d
	observation.elapsed, observation.phaseElapsed = now.Sub(d.started), now.Sub(d.phaseStarted)
	observation.admission = admission
	return &snapshotInstallError{cause: cause, diagnostic: observation}
}

// Only existing local failure logs inspect this envelope. Its text and unwrap
// preserve the original error and Connect status without adding wire details.
type snapshotInstallError struct {
	cause      error
	diagnostic snapshotInstallDiagnostic
}

func (e *snapshotInstallError) Error() string { return e.cause.Error() }
func (e *snapshotInstallError) Unwrap() error { return e.cause }

func snapshotFailureAttrs(err error) []any {
	var failure *snapshotInstallError
	if !errors.As(err, &failure) {
		return nil
	}
	d := failure.diagnostic
	attrs := []any{
		slog.String("snapshot_phase", d.phase),
		slog.Bool("snapshot_live_started", d.liveStarted),
		slog.Duration("snapshot_elapsed", d.elapsed),
		slog.Duration("snapshot_phase_elapsed", d.phaseElapsed),
		slog.Uint64("snapshot_staged_frames", d.stagedFrames),
		slog.Uint64("snapshot_staged_bytes", d.stagedBytes),
		slog.Uint64("snapshot_live_frames_read", d.liveFramesRead),
		slog.Bool("snapshot_cleanup_failed", d.cleanupFailed),
	}
	if d.admission.Reason != "" {
		attrs = append(attrs, slog.String("snapshot_last_admission_check", d.admission.Reason),
			slog.Bool("snapshot_last_admission_remaining_known", d.admission.RemainingKnown))
		if d.admission.RemainingKnown {
			attrs = append(attrs, slog.Duration("snapshot_last_admission_original_remaining", d.admission.OriginalBudgetRemaining))
		}
	}
	return attrs
}
