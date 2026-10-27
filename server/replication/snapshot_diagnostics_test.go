package replication

import (
	"bytes"
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
}
