package dataruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type failOncePublicationWAL struct{ writes int }

func (w *failOncePublicationWAL) Write(mutationlog.Entry) error {
	w.writes++
	if w.writes == 1 {
		return errors.New("injected WAL failure")
	}
	return nil
}

type heldPublicationWAL struct{ failed atomic.Bool }

func (w *heldPublicationWAL) Write(mutationlog.Entry) error {
	if w.failed.Load() {
		return errors.New("injected persistent WAL failure")
	}
	return nil
}

type countingCapacityBackend struct {
	Backend
	probes atomic.Int32
}

func (b *countingCapacityBackend) CapacityFootprint() (int, int) {
	b.probes.Add(1)
	return b.Backend.CapacityFootprint()
}

func snapshotInstallDiagnosticRows(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal("invalid publication diagnostic", err)
		}
		if row["level"] != "WARN" || row["msg"] != "publication fault transition" {
			t.Fatalf("unexpected publication diagnostic: %v", row)
		}
		delete(row, "time")
		delete(row, "level")
		delete(row, "msg")
		rows = append(rows, row)
	}
	return rows
}

func TestSnapshotInstallCompletionOwnsOneAdmittedAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("rejected begin %d", failAt), func(t *testing.T) {
			svc := NewExecutor(newFakeBackend())
			original := errors.New("Snapshot authority unavailable")
			checks := 0
			finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error {
				checks++
				if checks == failAt {
					return original
				}
				return nil
			})
			if finish != nil || err != original || checks != failAt || svc.activeSnapshotInstall.Load() != nil ||
				svc.SnapshotInstallActive() || svc.SnapshotRepairRequired() {
				t.Fatal("rejected begin published completion or retained installer custody", err)
			}
			if err := svc.waitForActiveSnapshotInstall(ctx); err != nil {
				t.Fatal("rejected begin introduced a public wait", err)
			}
		})
	}

	svc := NewExecutor(newFakeBackend())
	finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = finish(false) }()
	first := svc.activeSnapshotInstall.Load()
	if first == nil {
		t.Fatal("admitted begin did not publish completion")
	}
	select {
	case <-first.done:
		t.Fatal("active install was already complete")
	default:
	}
	if second, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil }); second != nil || connect.CodeOf(err) != connect.CodeUnavailable || svc.activeSnapshotInstall.Load() != first {
		t.Fatal("competing begin replaced the active attempt", err)
	}
	if err := finish(false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.done:
	default:
		t.Fatal("unverified finish did not release its waiters")
	}
	if svc.activeSnapshotInstall.Load() != nil || svc.SnapshotInstallActive() || !svc.SnapshotRepairRequired() {
		t.Fatal("completion cleared the repair fault or retained the active attempt")
	}
	if err := svc.waitForActiveSnapshotInstall(ctx); err != nil {
		t.Fatal("persistent fault was confused with an active attempt", err)
	}
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "refused"}}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("completion authorized a write after unverified finish", err)
	}

	retry, err := svc.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = retry(false) }()
	current := svc.activeSnapshotInstall.Load()
	if current == nil || current == first {
		t.Fatal("new install reused the old completion")
	}
	if err := finish(true); connect.CodeOf(err) != connect.CodeFailedPrecondition || svc.activeSnapshotInstall.Load() != current {
		t.Fatal("duplicate old finish detached the new attempt", err)
	}
	select {
	case <-current.done:
		t.Fatal("duplicate old finish completed the new attempt")
	default:
	}
	if err := retry(true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-current.done:
	default:
		t.Fatal("verified finish did not complete its own attempt")
	}
	if svc.activeSnapshotInstall.Load() != nil || svc.SnapshotInstallActive() || svc.SnapshotRepairRequired() {
		t.Fatal("verified retry retained active custody or the Snapshot fault")
	}
}

func TestSnapshotInstallCompletionPreservesRejectedFinish(t *testing.T) {
	for _, failAt := range []int{3, 4} {
		t.Run(fmt.Sprintf("authority check %d", failAt), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			svc := NewExecutor(newFakeBackend())
			original := connect.NewError(connect.CodePermissionDenied, errors.New("original authority expired"))
			checks := 0
			finish, err := svc.BeginSnapshotInstallChecked(ctx, func() error {
				checks++
				if checks == failAt {
					return original
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = finish(false) }()
			completion := svc.activeSnapshotInstall.Load()
			if completion == nil {
				t.Fatal("admitted attempt has no completion")
			}
			fault, count := svc.publicationFaultCh, svc.publicationFaultCount
			if err := finish(true); err != original || checks != failAt {
				t.Fatal("completion changed original validation/check semantics", err, checks)
			}
			select {
			case <-completion.done:
			default:
				t.Fatal("rejected finish retained public waiters")
			}
			if svc.activeSnapshotInstall.Load() != nil || svc.SnapshotInstallActive() || !svc.SnapshotRepairRequired() ||
				svc.publicationFaultCh != fault || svc.publicationFaultCount != count {
				t.Fatal("rejected finish changed repair authority or retained the attempt")
			}
			if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "refused"}}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal("rejected finish completion authorized a write", err)
			}
		})
	}
}

func TestSnapshotInstallCheckedRejectsAuthorityWithdrawnAcrossPublicationWait(t *testing.T) {
	for _, phase := range []string{"begin", "verified finish"} {
		for _, boundary := range []string{"public read latch", "publication gate", "acquired observer"} {
			for _, withdrawal := range []string{"caller canceled", "original admission expired"} {
				t.Run(phase+"/"+boundary+"/"+withdrawal, func(t *testing.T) {
					bounded, stop := context.WithTimeout(t.Context(), 5*time.Second)
					defer stop()
					ctx, cancel := context.WithCancel(bounded)
					defer cancel()
					observer := &testPublicationObserver{}
					var diagnostics bytes.Buffer
					logger := slog.New(slog.NewJSONHandler(&diagnostics, &slog.HandlerOptions{Level: slog.LevelWarn}))
					svc := NewExecutor(newFakeBackend()).WithHotPathMetrics(observer).WithLogger(logger)
					expired := errors.New("original Snapshot admission expired")
					var revoked, checkpoint atomic.Bool
					entered := make(chan struct{})
					check := func() error {
						if checkpoint.CompareAndSwap(true, false) {
							// This successful observation is already fixed when the
							// caller withdraws authority; only a later check may fail.
							close(entered)
							return nil
						}
						if revoked.Load() {
							return expired
						}
						return nil
					}
					var finish func(bool) error
					if phase == "verified finish" {
						var err error
						finish, err = svc.BeginSnapshotInstallChecked(ctx, check)
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = finish(false) }()
					}
					type installState struct {
						fault       <-chan struct{}
						count       int
						repair      bool
						generation  uint64
						certificate *publicationReadCertificate
					}
					state := func() installState {
						svc.replicationCutMu.RLock()
						defer svc.replicationCutMu.RUnlock()
						return installState{svc.publicationFaultCh, svc.publicationFaultCount, svc.snapshotInstallFaulted,
							svc.replicationCutMu.generation, svc.replicationCutMu.readCertificate.Load()}
					}
					before := state()
					var releaseOnce sync.Once
					var release func()
					var tryRead func() bool
					var unlockRead func()
					requested := make(chan struct{})
					acquired := make(chan struct{})
					if boundary == "public read latch" {
						svc.snapshotReadCutMu.RLock()
						release = func() { releaseOnce.Do(svc.snapshotReadCutMu.RUnlock) }
						tryRead, unlockRead = svc.snapshotReadCutMu.TryRLock, svc.snapshotReadCutMu.RUnlock
					} else if boundary == "publication gate" {
						svc.replicationCutMu.RLock()
						release = func() { releaseOnce.Do(svc.replicationCutMu.RUnlock) }
						tryRead, unlockRead = svc.replicationCutMu.TryRLock, svc.replicationCutMu.RUnlock
						var requestedOnce sync.Once
						svc.publicationClock = func() time.Time {
							if svc.publicationWaiting.count("snapshot") != 0 {
								requestedOnce.Do(func() { close(requested) })
							}
							return time.Now()
						}
					} else {
						resume := make(chan struct{})
						release = func() { releaseOnce.Do(func() { close(resume) }) }
						var clockCalls atomic.Int32
						svc.publicationClock = func() time.Time {
							if clockCalls.Add(1) == 2 {
								// Requested is the first observation; acquired is the
								// second and runs while the raw gate is exclusively owned.
								close(acquired)
								<-resume
							}
							return time.Now()
						}
					}
					defer release()
					checkpoint.Store(true)
					type outcome struct {
						finish func(bool) error
						err    error
					}
					done := make(chan outcome, 1)
					go func() {
						if phase == "begin" {
							f, err := svc.BeginSnapshotInstallChecked(ctx, check)
							done <- outcome{f, err}
							return
						}
						done <- outcome{err: finish(true)}
					}()
					select {
					case <-entered:
					case <-bounded.Done():
						t.Fatal("checked transition did not pass its successful precheck", bounded.Err())
					}
					if boundary == "publication gate" {
						select {
						case <-requested:
						case <-bounded.Done():
							t.Fatal("checked transition did not request the held publication cut", bounded.Err())
						}
					}
					if boundary == "acquired observer" {
						select {
						case <-acquired:
						case <-bounded.Done():
							t.Fatal("checked transition did not reach its acquired observation", bounded.Err())
						}
						if svc.replicationCutMu.TryRLock() {
							svc.replicationCutMu.RUnlock()
							t.Fatal("acquired observation did not hold the raw publication gate")
						}
					} else {
						// The original read lock remains held, so a failed TryRLock
						// proves this writer is queued rather than already owning the
						// boundary. Authority is withdrawn only after this checkpoint.
						for tryRead() {
							unlockRead()
							if bounded.Err() != nil {
								t.Fatal("checked transition did not queue at the held read boundary", bounded.Err())
							}
							runtime.Gosched()
						}
					}
					if !svc.SnapshotInstallActive() {
						t.Fatal("held transition lost its installer ownership")
					}
					select {
					case result := <-done:
						if result.finish != nil {
							_ = result.finish(false)
						}
						t.Fatalf("transition passed a still-held read boundary: %v", result.err)
					default:
					}
					want := expired
					if withdrawal == "caller canceled" {
						want = context.Canceled
						cancel()
					} else {
						revoked.Store(true)
					}
					release()
					select {
					case result := <-done:
						if result.finish != nil {
							_ = result.finish(false)
							t.Fatal("rejected begin returned an admitted finish handle")
						}
						if !errors.Is(result.err, want) {
							t.Fatalf("withdrawn authority was not preserved: %v, want %v", result.err, want)
						}
					case <-bounded.Done():
						t.Fatal("rejected transition did not release its held cut", bounded.Err())
					}
					if svc.SnapshotInstallActive() || state() != before || svc.SnapshotRepairRequired() != (phase == "verified finish") {
						t.Fatal("rejected transition changed fault, certificate or generation, or retained its install mutex")
					}
					select {
					case <-before.fault:
						if phase == "begin" {
							t.Fatal("rejected begin closed the healthy generation")
						}
					default:
						if phase == "verified finish" {
							t.Fatal("rejected finish replaced the original closed fault generation")
						}
					}
					if phase == "verified finish" {
						if err := finish(true); connect.CodeOf(err) != connect.CodeFailedPrecondition || state() != before {
							t.Fatalf("repeated rejected finish was not refused without changing state: %v", err)
						}
					}
					rows := snapshotInstallDiagnosticRows(t, &diagnostics)
					if phase == "begin" {
						if len(rows) != 0 || svc.snapshotInstallSequence != 0 {
							t.Fatalf("refused begin emitted an admitted transition: %v", rows)
						}
					} else if len(rows) != 2 || rows[1]["result"] == "verified_clear" ||
						rows[1]["attempt_id"] != rows[0]["attempt_id"] || rows[1]["snapshot_fault_remaining"] != true {
						t.Fatalf("refused finish diagnostic lost its admitted fault: %v", rows)
					}
					retry, err := svc.BeginSnapshotInstallChecked(bounded, func() error { return nil })
					if err != nil {
						t.Fatalf("rejected transition retained a lock: %v", err)
					}
					if err := retry(true); err != nil || svc.SnapshotInstallActive() || svc.SnapshotRepairRequired() {
						t.Fatalf("fresh admitted retry did not complete verified repair: %v", err)
					}
					after := state()
					if after.fault == before.fault || after.count != 0 || after.repair || !after.certificate.stable() || after.certificate.faulted {
						t.Fatal("successful control did not establish a new healthy generation")
					}
					if err := retry(true); connect.CodeOf(err) != connect.CodeFailedPrecondition || state() != after {
						t.Fatalf("repeated successful finish was not refused without publishing or unlocking twice: %v", err)
					}
				})
			}
		}
	}
}

func TestSnapshotInstallCheckedRechecksContextAfterBlockedFinalAuthorityCheck(t *testing.T) {
	for _, phase := range []string{"begin", "verified finish"} {
		t.Run(phase, func(t *testing.T) {
			bounded, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(bounded)
			defer cancel()
			svc := NewExecutor(newFakeBackend())
			var armed atomic.Bool
			var calls atomic.Int32
			held, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			check := func() error {
				if armed.Load() && calls.Add(1) == 2 {
					close(held)
					<-release
				}
				return nil // A delayed authority check may succeed after caller cancellation.
			}
			var finish func(bool) error
			if phase == "verified finish" {
				var err error
				finish, err = svc.BeginSnapshotInstallChecked(ctx, check)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = finish(false) }()
			}
			fault, count, repair := svc.publicationFaultCh, svc.publicationFaultCount, svc.snapshotInstallFaulted
			generation, certificate := svc.replicationCutMu.generation, svc.replicationCutMu.readCertificate.Load()
			armed.Store(true)
			type outcome struct {
				finish func(bool) error
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				if phase == "begin" {
					f, err := svc.BeginSnapshotInstallChecked(ctx, check)
					done <- outcome{f, err}
					return
				}
				done <- outcome{err: finish(true)}
			}()
			select {
			case <-held:
			case <-bounded.Done():
				t.Fatal("final authority check did not reach its exclusive publication cut", bounded.Err())
			}
			if svc.replicationCutMu.TryRLock() {
				svc.replicationCutMu.RUnlock()
				t.Fatal("final authority check did not hold the publication cut")
			}
			if svc.replicationCutMu.readCertificate.Load() != certificate {
				t.Fatal("final authority check published an active certificate before validation finished")
			}
			cancel()
			unblock()
			select {
			case result := <-done:
				if result.finish != nil {
					_ = result.finish(false)
					t.Fatal("canceled authority check returned an admitted finish handle")
				}
				if !errors.Is(result.err, context.Canceled) {
					t.Fatalf("successful late authority check hid caller cancellation: %v", result.err)
				}
			case <-bounded.Done():
				t.Fatal("canceled final authority check did not release its cuts", bounded.Err())
			}
			if svc.SnapshotInstallActive() || svc.SnapshotRepairRequired() != repair {
				t.Fatal("canceled final check changed repair authority or retained the install mutex")
			}
			svc.replicationCutMu.RLock()
			unchanged := svc.publicationFaultCh == fault && svc.publicationFaultCount == count &&
				svc.snapshotInstallFaulted == repair && svc.replicationCutMu.generation == generation &&
				svc.replicationCutMu.readCertificate.Load() == certificate
			svc.replicationCutMu.RUnlock()
			if !unchanged {
				t.Fatal("canceled final check changed fault, numeric generation or read certificate")
			}
			if !svc.snapshotReadCutMu.TryLock() {
				t.Fatal("canceled final check retained the public read latch")
			}
			svc.snapshotReadCutMu.Unlock()
		})
	}
}

func TestSnapshotInstall_FailedReplayKeepsCDCGapUntilVerifiedRetry(t *testing.T) {
	var diagnostics bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&diagnostics, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc := NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithLogger(logger)
	if svc.SnapshotInstallActive() {
		t.Fatal("fresh service reports an active Snapshot")
	}
	if svc.SnapshotRepairRequired() {
		t.Fatal("fresh service requires Snapshot repair")
	}
	oldGeneration, faulted := svc.publicationStatus()
	if faulted {
		t.Fatal("fresh service started with a CDC gap")
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !svc.SnapshotInstallActive() {
		t.Fatal("active Snapshot is not visible to the pump")
	}
	if !svc.SnapshotRepairRequired() {
		t.Fatal("active Snapshot does not require verified completion")
	}
	if _, err := svc.BeginSnapshotInstall(); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("concurrent Snapshot install error = %v", err)
	}
	select {
	case <-oldGeneration:
	default:
		t.Fatal("Snapshot install did not gap existing CDC generation")
	}
	finish(false)
	if svc.SnapshotInstallActive() {
		t.Fatal("failed Snapshot still reports an active installer")
	}
	if !svc.SnapshotRepairRequired() {
		t.Fatal("failed Snapshot no longer requires verified repair")
	}
	if _, faulted := svc.publicationStatus(); !faulted {
		t.Fatal("failed Snapshot replay cleared its CDC gap")
	}
	retry, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	retry(true)
	if svc.SnapshotInstallActive() {
		t.Fatal("verified Snapshot still reports an active installer")
	}
	if svc.SnapshotRepairRequired() {
		t.Fatal("verified Snapshot still requires repair")
	}
	newGeneration, faulted := svc.publicationStatus()
	if faulted || newGeneration == oldGeneration {
		t.Fatal("verified Snapshot replay did not create a healthy CDC generation")
	}
	select {
	case <-newGeneration:
		t.Fatal("verified Snapshot replay left new CDC generation closed")
	default:
	}
	want := []map[string]any{
		{"event": "snapshot_install_begin", "fault_source": "snapshot_install", "attempt_id": float64(1),
			"snapshot_first_set": true, "snapshot_fault_after": true, "receipt_fault_at_begin": false,
			"fault_count_before": float64(0), "fault_count_after": float64(1)},
		{"event": "snapshot_install_finish", "fault_source": "snapshot_install", "attempt_id": float64(1),
			"requested_verified": false, "result": "unverified", "snapshot_fault_remaining_known": true,
			"snapshot_fault_remaining": true},
		{"event": "snapshot_install_begin", "fault_source": "snapshot_install", "attempt_id": float64(2),
			"snapshot_first_set": false, "snapshot_fault_after": true, "receipt_fault_at_begin": false,
			"fault_count_before": float64(1), "fault_count_after": float64(1)},
		{"event": "snapshot_install_finish", "fault_source": "snapshot_install", "attempt_id": float64(2),
			"requested_verified": true, "result": "verified_clear", "snapshot_fault_remaining_known": true,
			"snapshot_fault_remaining": false, "receipt_fault_after": false, "fault_count_after": float64(0)},
	}
	if got := snapshotInstallDiagnosticRows(t, &diagnostics); !reflect.DeepEqual(got, want) {
		t.Fatalf("local Snapshot lifecycle diagnostics = %v, want %v", got, want)
	}
}

func TestSnapshotInstallDiagnosticsPreserveRefusalsAndErrors(t *testing.T) {
	t.Run("nil authority check", func(t *testing.T) {
		var diagnostics bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&diagnostics, &slog.HandlerOptions{Level: slog.LevelWarn}))
		svc := NewExecutor(newFakeBackend()).WithLogger(logger)
		finish, err := svc.BeginSnapshotInstallChecked(t.Context(), nil)
		if finish != nil || connect.CodeOf(err) != connect.CodeInvalidArgument || diagnostics.Len() != 0 ||
			svc.snapshotInstallSequence != 0 || svc.snapshotInstallFaulted || svc.publicationFaultCount != 0 {
			t.Fatal("nil check invented an admitted diagnostic transition", err)
		}
	})
	for _, failAt := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("authority_check_%d", failAt), func(t *testing.T) {
			var diagnostics bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&diagnostics, &slog.HandlerOptions{Level: slog.LevelWarn}))
			svc := NewExecutor(newFakeBackend()).WithLogger(logger)
			const secret = "private Snapshot authority sentinel"
			original := connect.NewError(connect.CodePermissionDenied, errors.New(secret))
			checks := 0
			check := func() error {
				checks++
				if checks == failAt {
					return original
				}
				return nil
			}
			fault := svc.publicationFaultCh
			finish, err := svc.BeginSnapshotInstallChecked(t.Context(), check)
			if failAt <= 2 {
				if finish != nil || err != original || !errors.Is(err, original) ||
					connect.CodeOf(err) != connect.CodePermissionDenied || checks != failAt ||
					svc.snapshotInstallSequence != 0 || svc.snapshotInstallFaulted || svc.publicationFaultCount != 0 ||
					svc.publicationFaultCh != fault || svc.SnapshotInstallActive() || diagnostics.Len() != 0 {
					t.Fatalf("refused begin changed error, checks or diagnostic state: err=%v checks=%d", err, checks)
				}
				return
			}
			if err != nil || finish == nil || checks != 2 {
				t.Fatalf("admitted begin = %v, checks=%d", err, checks)
			}
			generation, certificate := svc.replicationCutMu.generation, svc.replicationCutMu.readCertificate.Load()
			if err := finish(true); err != original || !errors.Is(err, original) ||
				connect.CodeOf(err) != connect.CodePermissionDenied || checks != failAt {
				t.Fatalf("refused finish changed original error or checks: err=%v checks=%d", err, checks)
			}
			if svc.SnapshotInstallActive() || !svc.snapshotInstallFaulted || svc.publicationFaultCount != 1 ||
				svc.publicationFaultCh != fault || svc.replicationCutMu.generation != generation ||
				svc.replicationCutMu.readCertificate.Load() != certificate {
				t.Fatal("refused finish changed fault/certificate state or retained the install mutex")
			}
			if err := finish(true); connect.CodeOf(err) != connect.CodeFailedPrecondition || checks != failAt {
				t.Fatalf("duplicate finish changed original once/check semantics: err=%v checks=%d", err, checks)
			}
			result := "validation_rejected"
			if failAt == 4 {
				result = "publication_gate_rejected"
			}
			rows := snapshotInstallDiagnosticRows(t, &diagnostics)
			want := map[string]any{"event": "snapshot_install_finish", "fault_source": "snapshot_install",
				"attempt_id": float64(1), "requested_verified": true, "result": result,
				"snapshot_fault_remaining_known": true, "snapshot_fault_remaining": true}
			if len(rows) != 2 || rows[0]["attempt_id"] != float64(1) || !reflect.DeepEqual(rows[1], want) ||
				strings.Contains(diagnostics.String(), secret) {
				t.Fatalf("refused finish invented current state, duplicate row or leaked error: %v", rows)
			}
		})
	}
}

func TestSnapshotInstallDiagnosticsVerifiedClearRetainsReceiptFault(t *testing.T) {
	var diagnostics bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&diagnostics, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc := NewExecutor(newFakeBackend()).WithLogger(logger)
	svc.replicationCutMu.Lock()
	svc.markReceiptCommitFaultLocked()
	svc.replicationCutMu.Unlock()
	fault := svc.publicationFaultCh
	finish, err := svc.BeginSnapshotInstallChecked(t.Context(), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	if svc.snapshotInstallFaulted || !svc.receiptCommitFaulted || svc.publicationFaultCount != 1 ||
		svc.publicationFaultCh != fault || !svc.replicationCutMu.readCertificate.Load().faulted {
		t.Fatal("Snapshot clear changed the independent receipt fault or its CDC generation")
	}
	rows := snapshotInstallDiagnosticRows(t, &diagnostics)
	if len(rows) != 3 || rows[0]["event"] != "receipt_commit_fault_set" ||
		rows[1]["receipt_fault_at_begin"] != true || rows[1]["fault_count_before"] != float64(1) ||
		rows[1]["fault_count_after"] != float64(2) {
		t.Fatalf("Snapshot begin did not sample its already-faulted boundary: %v", rows)
	}
	want := map[string]any{"event": "snapshot_install_finish", "fault_source": "snapshot_install",
		"attempt_id": float64(1), "requested_verified": true, "result": "verified_clear",
		"snapshot_fault_remaining_known": true, "snapshot_fault_remaining": false,
		"receipt_fault_after": true, "fault_count_after": float64(1)}
	if !reflect.DeepEqual(rows[2], want) {
		t.Fatalf("verified clear diagnostic claimed unrelated fault recovery: %v", rows[2])
	}
}

func TestSnapshotInstall_FaultBlocksPublicGraphWritesUntilVerifiedRetry(t *testing.T) {
	for _, replicated := range []bool{false, true} {
		name := "standalone"
		if replicated {
			name = "replicated"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			expiration := time.Now().Add(time.Hour)
			cache.PutVertexWithExpiration("kept", &pb.Vertex{Key: "kept", Value: &pb.Vertex_String_{String_: "original"}}, expiration)
			cache.PutEdgeWithExpiration("kept", "edge", 2, expiration)
			probe := &countingCapacityBackend{Backend: cache}
			svc := NewExecutor(probe).WithCapacityLimits(CapacityLimits{MaxVertices: 100, MaxEdges: 100})
			var log *mutationlog.Log
			var origin hlc.NodeID
			if replicated {
				log = mutationlog.New(mutationlog.Options{Capacity: 16})
				t.Cleanup(func() { _ = log.Close() })
				origin = hlc.NodeID{0x42}
				svc.WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
			}
			baselineProbes := probe.probes.Load()
			finish, err := svc.BeginSnapshotInstall()
			if err != nil {
				t.Fatal(err)
			}
			// A replay may already have applied an earlier valid frame.
			cache.PutEdgeWithExpiration("partial", "edge", 1, expiration)
			checks := []struct {
				name string
				call func() error
			}{
				{"PutVertices", func() error {
					_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "new"}}})
					return err
				}},
				{"PutVertices if_absent", func() error {
					_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{IfAbsent: true, Vertices: []*pb.Vertex{{Key: "new"}}})
					return err
				}},
				{"PutEdges outcomes", func() error {
					_, err := svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "kept", Head: "edge", Weight: 3}}})
					return err
				}},
				{"AddEdges effective weights", func() error {
					_, err := svc.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "kept", Head: "edge", Weight: 3}}})
					return err
				}},
				{"DeleteVertices outcomes", func() error {
					_, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"kept"}})
					return err
				}},
				{"DeleteEdges outcomes", func() error {
					_, err := svc.DeleteEdges(ctx, &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "kept", Head: "edge"}}})
					return err
				}},
				{"DeleteVerticesByPrefix", func() error {
					_, err := svc.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "kept"})
					return err
				}},
				{"DeleteEdgesByPrefix", func() error {
					_, err := svc.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "kept"})
					return err
				}},
				{"empty PutVertices", func() error {
					_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{})
					return err
				}},
				{"empty PutEdges", func() error {
					_, err := svc.PutEdges(ctx, &pb.PutEdgesRequest{})
					return err
				}},
				{"empty AddEdges", func() error {
					_, err := svc.AddEdges(ctx, &pb.AddEdgesRequest{})
					return err
				}},
				{"empty DeleteVertices", func() error {
					_, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{})
					return err
				}},
				{"empty DeleteEdges", func() error {
					_, err := svc.DeleteEdges(ctx, &pb.DeleteEdgesRequest{})
					return err
				}},
			}
			for _, check := range checks {
				t.Run(check.name, func(t *testing.T) {
					if err := check.call(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
						t.Fatalf("write during Snapshot fault = %v, want FailedPrecondition", err)
					}
					if got, live := cache.GetWeight("kept", "edge"); !live || got != 2 {
						t.Fatalf("write changed existing edge to (%v, %t)", got, live)
					}
					if got, live := cache.GetWeight("partial", "edge"); !live || got != 1 {
						t.Fatalf("write changed partially installed edge to (%v, %t)", got, live)
					}
					if value, live := cache.GetVertex("kept"); !live || value.GetString_() != "original" {
						t.Fatalf("write changed existing vertex to (%v, %t)", value, live)
					}
					if _, live := cache.GetVertex("new"); live {
						t.Fatal("write installed new vertex")
					}
					if got := probe.probes.Load(); got != baselineProbes {
						t.Fatalf("faulted write reached capacity probe %d times, want %d", got, baselineProbes)
					}
					if replicated && (svc.LocalSeq(origin) != 0 || log.Len() != 0) {
						t.Fatalf("write advanced origin/log to %d/%d", svc.LocalSeq(origin), log.Len())
					}
				})
			}
			finish(false)
			if _, err := svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "kept", Head: "edge", Weight: 4}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("write after failed Snapshot = %v, want FailedPrecondition", err)
			}
			if got := probe.probes.Load(); got != baselineProbes {
				t.Fatalf("write after failed Snapshot reached capacity probe %d times, want %d", got, baselineProbes)
			}
			retry, err := svc.BeginSnapshotInstall()
			if err != nil {
				t.Fatal(err)
			}
			retry(true)
			resp, err := svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "kept", Head: "edge", Weight: 4}})
			if err != nil || resp.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
				t.Fatalf("write after verified retry = (%v, %v)", resp, err)
			}
			if got := probe.probes.Load(); got == baselineProbes {
				t.Fatal("successful write never exercised a configured capacity probe")
			}
		})
	}
}

func TestSnapshotInstall_FaultBlocksReceiptWritesBeforeStoreOrGraph(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 16)
	ctx := context.Background()
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	origin := svc.clock.NodeID()
	checks := []struct {
		name string
		call func() error
	}{
		{"PutVertices", func() error {
			_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{
				Vertices:       []*pb.Vertex{{Key: "rejected"}},
				ReceiptContext: publicReceiptContext(t, runtime, 0x91, 1),
			})
			return err
		}},
		{"AddEdges", func() error {
			_, err := svc.AddEdges(ctx, publicReceiptEdgeAddRequest(t, runtime, 0x92,
				&pb.Edge{Tail: "rejected", Head: "edge", Weight: 1}))
			return err
		}},
		{"DeleteVertices", func() error {
			_, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{
				Keys: []string{"rejected"}, ReceiptContext: publicReceiptContext(t, runtime, 0x93, 1),
			})
			return err
		}},
		{"DeleteEdges", func() error {
			_, err := svc.DeleteEdges(ctx, &pb.DeleteEdgesRequest{
				Edges:          []*pb.EdgeKey{{Tail: "rejected", Head: "edge"}},
				ReceiptContext: publicReceiptContext(t, runtime, 0x94, 1),
			})
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("receipt write during Snapshot fault = %v, want FailedPrecondition", err)
			}
			snapshot, err := runtime.receipt.store.Snapshot()
			if err != nil || len(snapshot.Receipts) != 0 {
				t.Fatalf("receipt write changed Store: %+v, %v", snapshot.Receipts, err)
			}
			if _, live := runtime.graph.GetVertex("rejected"); live {
				t.Fatal("receipt write changed graph")
			}
			if svc.LocalSeq(origin) != 0 || runtime.log.Len() != 0 {
				t.Fatalf("receipt write changed origin/log to %d/%d",
					svc.LocalSeq(origin), runtime.log.Len())
			}
		})
	}
	finish(false)
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices:       []*pb.Vertex{{Key: "rejected"}},
		ReceiptContext: publicReceiptContext(t, runtime, 0x95, 1),
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("receipt write after failed Snapshot = %v, want FailedPrecondition", err)
	}
}

func TestPublishLocalMutation_FaultBlocksLaterWritesAndRepairsOriginal(t *testing.T) {
	ctx := context.Background()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	wal := &heldPublicationWAL{}
	wal.failed.Store(true)
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("local")
	svc := NewExecutor(cache).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	if svc.CanSkipSelfEcho(origin) || svc.CanSkipSelfEcho(bytes16("different")) {
		t.Fatal("uncommitted or different local origin permitted self-echo suppression")
	}
	first := &pb.PutVerticesRequest{IfAbsent: true, Vertices: []*pb.Vertex{{
		Key: "first", Value: &pb.Vertex_String_{String_: "original"}, Expiration: futureTs(time.Minute),
	}}}
	if _, err := svc.PutVertices(ctx, first); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("first PutVertices error = %v, want Unavailable", err)
	}
	if value, ok := cache.GetVertex("first"); !ok || value.GetString_() != "original" {
		t.Fatalf("first graph effect = (%v,%v), want original", value, ok)
	}
	if svc.pendingLocalMutation == nil || svc.pendingLocalMutation.mutation.GetSeq() != 1 {
		t.Fatalf("pending local mutation = %v, want seq 1", svc.pendingLocalMutation)
	}
	if svc.CanSkipSelfEcho(origin) {
		t.Fatal("unpublished local mutation permitted self-echo suppression")
	}
	pendingEffect := svc.pendingLocalMutation.walOp
	if _, ok := pendingEffect.(*graphPutEffectEnvelope); !ok {
		t.Fatalf("pending local WAL evidence = %T, want graph Put effect", pendingEffect)
	}
	if got := svc.LocalSeq(origin); got != 0 {
		t.Fatalf("fault advanced origin seq to %d", got)
	}
	if got := log.Len(); got != 0 {
		t.Fatalf("fault published %d entries", got)
	}
	if err := svc.ApplySnapshotWatermarks(map[string]uint64{hex.EncodeToString(origin[:]): 1}, hlc.Timestamp{}); err == nil {
		t.Fatal("snapshot watermark skipped an unpublished local mutation")
	}
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"first"}}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("write while repair fails = %v, want Unavailable", err)
	}
	if _, ok := cache.GetVertex("first"); !ok {
		t.Fatal("later Delete changed graph before repair")
	}
	first.Vertices[0] = &pb.Vertex{Key: "caller-mutated", Value: &pb.Vertex_String_{String_: "caller-mutated"}}
	wal.failed.Store(false)
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	if svc.CanSkipSelfEcho(origin) {
		t.Fatal("active Snapshot permitted self-echo suppression")
	}
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "repair-probe"}}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("write repaired pending WAL during Snapshot fault: %v", err)
	}
	if svc.pendingLocalMutation == nil || svc.pendingLocalMutation.walOp != pendingEffect || log.Len() != 0 {
		t.Fatal("Snapshot-faulted write changed pending WAL repair state")
	}
	finish(true)
	if err := svc.ApplyMutation(ctx, cloneQueuedMutation(svc.pendingLocalMutation.mutation)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("remote self-echo crossed local publication fault: %v", err)
	}
	response, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{IfAbsent: true, Vertices: []*pb.Vertex{{
		Key: "first", Value: &pb.Vertex_String_{String_: "replacement"}, Expiration: futureTs(time.Minute),
	}}})
	if err != nil {
		t.Fatalf("repairing conditional Put: %v", err)
	}
	if got := response.GetOutcomes(); len(got) != 1 || got[0] != pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET {
		t.Fatalf("retry outcomes = %v, want CONDITION_NOT_MET", got)
	}
	if value, ok := cache.GetVertex("first"); !ok || value.GetString_() != "original" {
		t.Fatalf("repair reapplied conditional Put: (%v,%v)", value, ok)
	}
	if svc.pendingLocalMutation != nil || svc.publicationFaultCount != 0 {
		t.Fatal("repair left local publication fault set")
	}
	if !svc.CanSkipSelfEcho(origin) {
		t.Fatal("verified recovery did not restore safe self-echo suppression")
	}
	if got := svc.LocalSeq(origin); got != 1 {
		t.Fatalf("repaired origin seq = %d, want 1", got)
	}
	entries, cancel, err := log.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cancel() }()
	repairedEntry := <-entries
	if repairedEntry.Op != pendingEffect {
		t.Fatal("repair replaced the exact retained WAL envelope")
	}
	mutation := mustGraphMutation(t, repairedEntry.Op)
	live := mutation.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetLive()
	if mutation.GetSeq() != 1 || live.GetKey() != "first" || live.GetString_() != "original" {
		t.Fatalf("repaired original mutation = %v", mutation)
	}
}

func TestCanSkipSelfEchoNeverSkipsDurableReceiptOrigin(t *testing.T) {
	fixture := newReceiptEdgeDeleteFixture(t, nil)
	if fixture.service.CanSkipSelfEcho(fixture.service.clock.NodeID()) {
		t.Fatal("receipt-WAL origin permitted self-echo suppression")
	}
}

func TestCanSkipSelfEchoRequiresCommittedLocalHistory(t *testing.T) {
	origin := bytes16("echo-origin")
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	if svc.CanSkipSelfEcho(origin) {
		t.Fatal("empty local origin permitted self-echo suppression")
	}
	if _, err := svc.PutVertex(context.Background(), &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: "own", Expiration: futureTs(time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	if !svc.CanSkipSelfEcho(origin) {
		t.Fatal("committed graph-only origin could not suppress self-echo")
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	if svc.CanSkipSelfEcho(origin) {
		t.Fatal("Snapshot fault permitted self-echo suppression")
	}
	finish(true)
	if !svc.CanSkipSelfEcho(origin) {
		t.Fatal("verified Snapshot did not restore self-echo suppression")
	}
}

func TestPublishLocalDeleteRepairKeepsOriginalDeadline(t *testing.T) {
	ctx := context.Background()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	cache.PutVertexWithExpiration("victim", &pb.Vertex{Key: "victim"}, time.Now().Add(time.Hour))
	wal := &heldPublicationWAL{}
	wal.failed.Store(true)
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("delete-repair")
	svc := NewExecutor(cache).WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"victim"}}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Delete WAL failure = %v, want Unavailable", err)
	}
	pending := svc.pendingLocalMutation
	tombstones := cache.SnapshotReplication().Tombstones.Vertices
	if pending == nil || pending.mutation.GetTombstoneExpiration() == nil || len(tombstones) != 1 ||
		!pending.mutation.GetTombstoneExpiration().AsTime().Equal(tombstones[0].Expiration) {
		t.Fatalf("pending Delete deadline differs from applied tombstone: pending=%v tombstones=%+v", pending, tombstones)
	}
	wal.failed.Store(false)
	if _, err := svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "repair"}}); err != nil {
		t.Fatalf("repairing next write: %v", err)
	}
	entries, cancel, err := log.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	repaired := mustGraphMutation(t, (<-entries).Op)
	if repaired.GetSeq() != 1 || !repaired.GetTombstoneExpiration().AsTime().Equal(tombstones[0].Expiration) {
		t.Fatalf("repaired Delete renewed deadline: %v", repaired)
	}
}

func TestPublishLocalMutation_BornExpiredPutKeepsBarrier(t *testing.T) {
	ctx := context.Background()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	wal := &failOncePublicationWAL{}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("local")
	svc := NewExecutor(cache).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
		Key: "expired", Expiration: futureTs(-time.Second),
	}}})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("born-expired Put error = %v, want Unavailable", err)
	}
	if _, ok := cache.GetVertex("expired"); ok {
		t.Fatal("born-expired Put became live")
	}
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "next"}}}); err != nil {
		t.Fatalf("repair and next Put: %v", err)
	}
	if got := svc.LocalSeq(origin); got != 2 {
		t.Fatalf("origin seq = %d, want 2", got)
	}
	entries, cancel, err := log.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cancel() }()
	first := mustGraphMutation(t, (<-entries).Op)
	if first.GetSeq() != 1 || first.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetCausalBarrier().GetKey() != "expired" {
		t.Fatalf("repaired born-expired mutation = %v", first)
	}
	second := mustGraphMutation(t, (<-entries).Op)
	if second.GetSeq() != 2 || second.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetLive().GetKey() != "next" {
		t.Fatalf("next mutation = %v", second)
	}
}

func TestPublishRemoteMutation_FailedAppendRetriesWithoutDoubleApply(t *testing.T) {
	// fakeBackend's AddEdge is deliberately non-idempotent, so a repeated
	// backend call after the WAL failure would visibly double the weight.
	cache := newFakeBackend()
	wal := &failOncePublicationWAL{}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	local, origin := bytes16("local"), bytes16("remote")
	svc := NewExecutor(cache).WithReplication(log, hlc.New(local, hlc.Options{}), nil)
	exp := futureTs(time.Hour)
	m := &pb.Mutation{Seq: 1, Origin: origin[:], Hlc: newHLC(1, origin), Op: &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
		AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 2, Expiration: exp}},
	}}}
	if code := connect.CodeOf(svc.ApplyMutation(context.Background(), m)); code != connect.CodeUnavailable {
		t.Fatalf("failed append code = %v, want Unavailable", code)
	}
	if got := svc.LocalSeq(origin); got != 0 {
		t.Fatalf("failed append advanced cursor to %d", got)
	}
	if got := log.Len(); got != 0 {
		t.Fatalf("failed append published %d entries", got)
	}
	if weight := cache.edges["a"]["b"]; weight != 2 {
		t.Fatalf("first graph apply weight = %v, want 2", weight)
	}
	pending := svc.pendingMutations[origin][1]
	if pending == nil {
		t.Fatal("failed remote append did not retain pending mutation")
	}
	pendingEffect := pending.walOp
	if _, ok := pendingEffect.(*graphAddEffectEnvelope); !ok {
		t.Fatalf("pending remote WAL evidence = %T, want graph Add effect", pendingEffect)
	}
	if err := svc.ApplyMutation(context.Background(), m); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := svc.LocalSeq(origin); got != 1 {
		t.Fatalf("retried cursor = %d, want 1", got)
	}
	if got := log.Len(); got != 1 {
		t.Fatalf("retried log len = %d, want 1", got)
	}
	if weight := cache.edges["a"]["b"]; weight != 2 {
		t.Fatalf("retry applied Add twice: weight=%v", weight)
	}
	if entry := mustMutationLogEntry(t, log, 1); entry.Op != pendingEffect {
		t.Fatal("remote retry replaced the exact retained WAL envelope")
	}
}

func TestPublishRemoteMutation_SnapshotFaultRetainsPendingEffect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cutoff    uint64
		failDrain bool
		wantLog   int
	}{
		{name: "pending above cutoff", cutoff: 0, wantLog: 2},
		{name: "pending covered by cutoff", cutoff: 1, wantLog: 1},
		{name: "post-cutoff drain fails then recovers", cutoff: 1, failDrain: true, wantLog: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cache := newFakeBackend()
			wal := &heldPublicationWAL{}
			wal.failed.Store(true)
			log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
			t.Cleanup(func() { _ = log.Close() })
			local, origin := bytes16("local"), bytes16("remote")
			svc := NewExecutor(cache).WithReplication(log, hlc.New(local, hlc.Options{}), nil)
			mutation := func(seq uint64, weight float32) *pb.Mutation {
				return &pb.Mutation{
					Seq: seq, Origin: origin[:], Hlc: newHLC(int64(seq), origin),
					Op: &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
						Edge: &pb.Edge{Tail: "a", Head: "b", Weight: weight, Expiration: futureTs(time.Hour)},
					}}},
				}
			}
			first, future, later := mutation(1, 2), mutation(2, 3), mutation(3, 4)
			if err := svc.ApplyMutation(ctx, future); err != nil {
				t.Fatalf("queue future seq: %v", err)
			}
			if code := connect.CodeOf(svc.ApplyMutation(ctx, first)); code != connect.CodeUnavailable {
				t.Fatalf("first relay append = %v, want Unavailable", code)
			}
			pendingFirst := svc.pendingMutations[origin][1]
			pendingFuture := svc.pendingMutations[origin][2]
			if pendingFirst == nil || !pendingFirst.applied || !pendingFirst.faulted ||
				pendingFirst.walOp == nil || pendingFuture == nil || pendingFuture.applied {
				t.Fatalf("accepted remote effects after WAL failure = %+v / %+v", pendingFirst, pendingFuture)
			}
			retainedWAL := pendingFirst.walOp
			pendingBytes := svc.pendingBytes
			checkRejected := func(phase string) {
				t.Helper()
				for _, m := range []*pb.Mutation{first, future, later} {
					err := svc.ApplyMutation(ctx, m)
					if connect.CodeOf(err) != connect.CodeFailedPrecondition {
						t.Fatalf("%s peer seq %d = %v, want FailedPrecondition", phase, m.GetSeq(), err)
					}
					if svc.pendingMutations[origin][1] != pendingFirst ||
						svc.pendingMutations[origin][2] != pendingFuture ||
						svc.pendingMutations[origin][3] != nil ||
						svc.pendingCount != 2 || svc.pendingBytes != pendingBytes ||
						pendingFirst.walOp != retainedWAL {
						t.Fatalf("%s changed accepted pending remote effects", phase)
					}
					if got := cache.edges["a"]["b"]; got != 2 ||
						svc.LocalSeq(origin) != 0 || log.Len() != 0 {
						t.Fatalf("%s peer seq %d changed graph/origin/log to %v/%d/%d",
							phase, m.GetSeq(), got, svc.LocalSeq(origin), log.Len())
					}
				}
			}
			finish, err := svc.BeginSnapshotInstall()
			if err != nil {
				t.Fatal(err)
			}
			checkRejected("active Snapshot")
			finish(false)
			checkRejected("failed Snapshot")

			retry, err := svc.BeginSnapshotInstall()
			if err != nil {
				t.Fatal(err)
			}
			if !tc.failDrain {
				wal.failed.Store(false)
			}
			cutoffs := map[string]uint64{hex.EncodeToString(origin[:]): tc.cutoff}
			snapshotHLC := hlc.Timestamp{WallNs: 1, NodeID: origin}
			err = svc.ApplySnapshotWatermarks(cutoffs, snapshotHLC)
			if tc.failDrain {
				if connect.CodeOf(err) != connect.CodeUnavailable {
					t.Fatalf("post-cutoff pending append = %v, want Unavailable", err)
				}
				if got := cache.edges["a"]["b"]; got != 5 ||
					svc.LocalSeq(origin) != 1 || log.Len() != 0 ||
					svc.pendingMutations[origin][2] != pendingFuture ||
					!pendingFuture.applied {
					t.Fatalf("failed installer drain lost pending effect: weight=%v seq/log=%d/%d pending=%+v",
						got, svc.LocalSeq(origin), log.Len(), pendingFuture)
				}
				retry(false)
				if _, faulted := svc.publicationStatus(); !faulted {
					t.Fatal("failed installer drain exposed a healthy read cut")
				}
				if code := connect.CodeOf(svc.ApplyMutation(ctx, later)); code != connect.CodeFailedPrecondition {
					t.Fatalf("peer after failed drain = %v, want FailedPrecondition", code)
				}
				wal.failed.Store(false)
				retry, err = svc.BeginSnapshotInstall()
				if err != nil {
					t.Fatal(err)
				}
				err = svc.ApplySnapshotWatermarks(cutoffs, snapshotHLC)
			}
			if err != nil {
				retry(false)
				t.Fatalf("verified installer watermark drain: %v", err)
			}
			retry(true)
			if got := cache.edges["a"]["b"]; got != 5 ||
				svc.LocalSeq(origin) != 2 || log.Len() != tc.wantLog ||
				svc.pendingCount != 0 || svc.pendingBytes != 0 || svc.publicationFaultCount != 0 {
				t.Fatalf("verified retry state: weight=%v seq/log=%d/%d pending=%d/%d faults=%d",
					got, svc.LocalSeq(origin), log.Len(), svc.pendingCount, svc.pendingBytes, svc.publicationFaultCount)
			}
			if tc.cutoff == 0 && mustMutationLogEntry(t, log, 1).Op != retainedWAL {
				t.Fatal("above-cutoff repair replaced the original WAL envelope")
			}
			if tc.cutoff == 1 && mustGraphMutation(t, mustMutationLogEntry(t, log, 1).Op).GetSeq() != 2 {
				t.Fatal("covered pending effect was re-appended after Snapshot cutoff")
			}
			if err := svc.ApplyMutation(ctx, first); err != nil {
				t.Fatalf("covered/retained duplicate seq 1: %v", err)
			}
			if err := svc.ApplyMutation(ctx, future); err != nil {
				t.Fatalf("retained duplicate seq 2: %v", err)
			}
			if got := cache.edges["a"]["b"]; got != 5 || log.Len() != tc.wantLog {
				t.Fatalf("duplicate after verified retry reapplied graph/log to %v/%d", got, log.Len())
			}
			if err := svc.ApplyMutation(ctx, later); err != nil {
				t.Fatalf("new remote mutation after verified retry: %v", err)
			}
			if got := cache.edges["a"]["b"]; got != 9 ||
				svc.LocalSeq(origin) != 3 || log.Len() != tc.wantLog+1 {
				t.Fatalf("new peer effect after retry = weight=%v seq/log=%d/%d",
					got, svc.LocalSeq(origin), log.Len())
			}
		})
	}
}

func TestPublishRemoteMutationAdvancesClockAfterContiguousCommit(t *testing.T) {
	base := time.Now()
	local, origin := bytes16("local-clock"), bytes16("remote-clock")
	clock := hlc.New(local, hlc.Options{Now: func() int64 { return base.UnixNano() }})
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewExecutor(cache).WithReplication(log, clock, nil)
	mutation := func(seq uint64, stamp hlc.Timestamp, key string) *pb.Mutation {
		return &pb.Mutation{
			Seq: seq, Origin: origin[:], Hlc: hlcToProto(stamp),
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
				Vertex: &pb.Vertex{Key: key, Expiration: timestamppb.New(base.Add(time.Hour))},
			}}},
		}
	}
	firstStamp := hlc.Timestamp{WallNs: base.Add(100 * time.Millisecond).UnixNano(), Logical: 1, NodeID: origin}
	secondStamp := hlc.Timestamp{WallNs: base.Add(200 * time.Millisecond).UnixNano(), Logical: 2, NodeID: origin}
	if err := svc.ApplyMutation(context.Background(), mutation(2, secondStamp, "second")); err != nil {
		t.Fatal(err)
	}
	if queuedClock := clock.Now(); !queuedClock.Less(secondStamp) {
		t.Fatalf("queued remote mutation advanced clock to %v before publication", queuedClock)
	}
	if err := svc.ApplyMutation(context.Background(), mutation(1, firstStamp, "first")); err != nil {
		t.Fatal(err)
	}
	if got := svc.LocalSeq(origin); got != 2 {
		t.Fatalf("contiguous remote frontier = %d, want 2", got)
	}
	if next := clock.Now(); !secondStamp.Less(next) {
		t.Fatalf("local clock %v did not advance beyond published remote HLC %v", next, secondStamp)
	}
	futureStamp := hlc.Timestamp{
		WallNs: base.Add(2 * hlc.DefaultMaxSkew).UnixNano(),
		NodeID: origin,
	}
	if err := svc.ApplyMutation(context.Background(), mutation(3, futureStamp, "graph-only-future")); err != nil {
		t.Fatalf("graph-only future HLC changed behavior: %v", err)
	}
	if next := clock.Now(); !next.Less(futureStamp) {
		t.Fatalf("graph-only clock %v did not preserve skew clamping below %v", next, futureStamp)
	}
}

func TestPublishRemoteMutationDurableClockFloorAfterWallRollback(t *testing.T) {
	base := time.Now()
	highWater := base.Add(hlc.DefaultMaxSkew).Truncate(time.Millisecond)
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: highWater,
	}
	origin := hlc.NodeID{0x58}
	f := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
		t, nil, hlc.NodeID{0x59}, config,
		hlc.Options{Now: func() int64 { return base.UnixNano() }},
	)
	stamp := hlc.Timestamp{
		WallNs:  highWater.Add(hlc.DefaultMaxSkew / 2).UnixNano(),
		Logical: 7,
		NodeID:  origin,
	}
	future := timestamppb.New(time.Unix(0, stamp.WallNs).Add(time.Hour))
	first := &pb.Mutation{
		Seq: 1, Origin: origin[:], Hlc: hlcToProto(stamp),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "rollback-put", Expiration: future},
		}}},
	}
	lastStamp := stamp
	lastStamp.Logical++
	second := &pb.Mutation{
		Seq: 2, Origin: origin[:], Hlc: hlcToProto(lastStamp),
		Op: &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "rollback", Head: "add", Weight: 1, Expiration: future},
		}}},
	}
	if err := f.service.ApplyMutation(context.Background(), second); err != nil {
		t.Fatalf("queue durable remote mutation after wall rollback: %v", err)
	}
	if got := f.coordinator.store.Stats().HighWaterMillis; got != highWater.UnixMilli() {
		t.Fatalf("queued remote mutation advanced Store high-water to %d, want %d", got, highWater.UnixMilli())
	}
	hostile := lastStamp
	hostile.WallNs = highWater.Add(hlc.DefaultMaxSkew + time.Nanosecond).UnixNano()
	hostile.Logical++
	err := f.service.ApplyMutation(context.Background(), &pb.Mutation{
		Seq: 3, Origin: origin[:], Hlc: hlcToProto(hostile),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "hostile-future", Expiration: future},
		}}},
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("hostile future HLC after queued frame = %v, want InvalidArgument", err)
	}
	if err := f.service.ApplyMutation(context.Background(), first); err != nil {
		t.Fatalf("drain durable remote mutations after wall rollback: %v", err)
	}
	if got := f.coordinator.store.Stats().HighWaterMillis; got < lastStamp.WallNs/int64(time.Millisecond) {
		t.Fatalf("committed remote mutation left Store high-water at %d, want at least %d", got, lastStamp.WallNs/int64(time.Millisecond))
	}
	states := f.service.OriginStates()
	if len(states) != 1 || states[0].Origin != origin || states[0].LastSeq != 2 ||
		states[0].LastHLC != lastStamp {
		t.Fatalf("durable remote origin state = %+v", states)
	}

	source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.Capture(context.Background(), config)
	if err != nil {
		t.Fatalf("receipt capture after generic remote publication: %v", err)
	}
	if len(capture.Graph) == 0 || capture.Graph[0].GetHeader() == nil {
		t.Fatalf("receipt capture has no header: %+v", capture.Graph)
	}
	cutoff := hlcFromProto(capture.Graph[0].GetHeader().GetCutoffHlc())
	if !lastStamp.Less(cutoff) {
		t.Fatalf("receipt capture cutoff %v did not exceed generic origin HLC %v", cutoff, lastStamp)
	}
}

func TestPublishRemoteMutationDurablePreflightPreservesClockHighWater(t *testing.T) {
	base := time.Now().Truncate(time.Millisecond)
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: base,
	}
	f := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
		t, nil, hlc.NodeID{0x69}, config,
		hlc.Options{Now: func() int64 { return base.UnixNano() }},
	)
	future := timestamppb.New(base.Add(time.Hour))
	invalidExpiration := &timestamppb.Timestamp{Seconds: 253402300800}
	tests := []struct {
		name   string
		origin hlc.NodeID
		op     *pb.MutationOp
	}{
		{
			name:   "nil singular payload",
			origin: hlc.NodeID{0x70},
			op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{},
			}},
		},
		{
			name:   "empty plural payload",
			origin: hlc.NodeID{0x71},
			op: &pb.MutationOp{Op: &pb.MutationOp_AddEdges{
				AddEdges: &pb.AddEdgesRequest{},
			}},
		},
		{
			name:   "invalid expiration",
			origin: hlc.NodeID{0x72},
			op: &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
				PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{
					Tail: "invalid", Head: "expiration", Expiration: invalidExpiration,
				}},
			}},
		},
		{
			name:   "invalid contribution identity",
			origin: hlc.NodeID{0x73},
			op: &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
				AddEdge: &pb.AddEdgeRequest{
					Edge:      &pb.Edge{Tail: "invalid", Head: "contribution", Expiration: future},
					ContribId: []byte{1},
				},
			}},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stamp := hlc.Timestamp{
				WallNs:  base.Add(time.Duration(i+1) * 50 * time.Millisecond).UnixNano(),
				Logical: uint32(i + 1),
				NodeID:  tt.origin,
			}
			err := f.service.ApplyMutation(context.Background(), &pb.Mutation{
				Seq: 1, Origin: tt.origin[:], Hlc: hlcToProto(stamp), Op: tt.op,
			})
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid durable mutation error = %v, want InvalidArgument", err)
			}
			if got := f.coordinator.store.Stats().HighWaterMillis; got != base.UnixMilli() {
				t.Fatalf("invalid durable mutation advanced Store high-water to %d, want %d", got, base.UnixMilli())
			}
			if got := f.service.LocalSeq(tt.origin); got != 0 {
				t.Fatalf("invalid durable mutation advanced origin to %d", got)
			}
			if pending := f.service.pendingMutations[tt.origin]; len(pending) != 0 {
				t.Fatalf("invalid durable mutation remained queued: %+v", pending)
			}
		})
	}
	if got := f.log.Len(); got != 0 {
		t.Fatalf("invalid durable mutations appended %d relay rows", got)
	}
	if _, live := f.cache.GetWeight("invalid", "expiration"); live {
		t.Fatal("invalid durable mutation changed the graph")
	}
	if _, err := f.service.AddEdges(context.Background(), &pb.AddEdgesRequest{}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty local durable AddEdges error = %v, want InvalidArgument", err)
	}
	if _, err := f.service.AddEdge(context.Background(), &pb.AddEdgeRequest{
		Edge:      &pb.Edge{Tail: "local", Head: "invalid-contribution", Expiration: future},
		ContribId: []byte{1},
	}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid local durable contribution identity error = %v, want InvalidArgument", err)
	}
	if got := f.log.Len(); got != 0 {
		t.Fatalf("invalid local durable mutations appended %d relay rows", got)
	}

	origin := hlc.NodeID{0x74}
	stamp := hlc.Timestamp{
		WallNs:  base.Add(250 * time.Millisecond).UnixNano(),
		Logical: 5,
		NodeID:  origin,
	}
	if err := f.service.ApplyMutation(context.Background(), &pb.Mutation{
		Seq: 1, Origin: origin[:], Hlc: hlcToProto(stamp),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "valid", Expiration: future}},
		}},
	}); err != nil {
		t.Fatalf("valid durable mutation after rejected frames: %v", err)
	}
	if got := f.coordinator.store.Stats().HighWaterMillis; got < stamp.WallNs/int64(time.Millisecond) {
		t.Fatalf("valid durable mutation left Store high-water at %d, want at least %d", got, stamp.WallNs/int64(time.Millisecond))
	}
	if _, live := f.cache.GetVertex("valid"); !live {
		t.Fatal("valid durable mutation did not change the graph")
	}
	if got := f.service.LocalSeq(origin); got != 1 {
		t.Fatalf("valid durable mutation advanced origin to %d, want 1", got)
	}
	if got := f.log.Len(); got != 1 {
		t.Fatalf("valid durable mutation appended %d relay rows, want 1", got)
	}
}

func TestGraphOnlyPublicationPreservesEmptyAddBatch(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	service := NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).
		WithReplication(log, hlc.New(hlc.NodeID{0x75}, hlc.Options{}), nil)
	if _, err := service.AddEdges(context.Background(), &pb.AddEdgesRequest{}); err != nil {
		t.Fatalf("graph-only empty AddEdges: %v", err)
	}
	if got := log.Len(); got != 1 {
		t.Fatalf("graph-only empty AddEdges appended %d rows, want 1", got)
	}
}

func TestPublishRemoteMutationDurableRevalidatesOwnedQueuedFrame(t *testing.T) {
	base := time.Now().Truncate(time.Millisecond)
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: base,
	}
	f := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
		t, nil, hlc.NodeID{0x76}, config,
		hlc.Options{Now: func() int64 { return base.UnixNano() }},
	)
	origin := hlc.NodeID{0x77}
	future := timestamppb.New(base.Add(time.Hour))
	mutation := func(seq uint64, offset time.Duration, key string) *pb.Mutation {
		stamp := hlc.Timestamp{
			WallNs: base.Add(offset).UnixNano(), Logical: uint32(seq), NodeID: origin,
		}
		return &pb.Mutation{
			Seq: seq, Origin: origin[:], Hlc: hlcToProto(stamp),
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Expiration: future}},
			}},
		}
	}
	if err := f.service.ApplyMutation(context.Background(), mutation(2, 200*time.Millisecond, "second")); err != nil {
		t.Fatalf("queue future durable mutation: %v", err)
	}
	queued := f.service.pendingMutations[origin][2]
	if queued == nil {
		t.Fatal("future durable mutation was not queued")
	}
	queued.mutation.GetOp().GetPutVertex().Vertex = nil

	err := f.service.ApplyMutation(context.Background(), mutation(1, 100*time.Millisecond, "first"))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("owned queued revalidation error = %v, want InvalidArgument", err)
	}
	firstStamp := base.Add(100 * time.Millisecond).UnixMilli()
	if got := f.coordinator.store.Stats().HighWaterMillis; got != firstStamp {
		t.Fatalf("rejected queued frame advanced Store high-water to %d, want %d", got, firstStamp)
	}
	if got := f.service.LocalSeq(origin); got != 1 {
		t.Fatalf("rejected queued frame advanced origin to %d, want 1", got)
	}
	if got := f.log.Len(); got != 1 {
		t.Fatalf("rejected queued frame appended %d relay rows, want 1", got)
	}
	if _, live := f.cache.GetVertex("first"); !live {
		t.Fatal("first contiguous frame did not commit")
	}
	if _, live := f.cache.GetVertex("second"); live {
		t.Fatal("rejected queued frame changed the graph")
	}
	if pending := f.service.pendingMutations[origin][2]; pending != queued {
		t.Fatal("rejected queued frame did not retain exact pending evidence")
	}
}

func TestLocalGraphWritesPublishEffectCompleteEnvelopes(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 16, SubscriberBuffer: 16})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("local-effects")
	svc := NewExecutor(cache).WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	ctx := context.Background()
	future := timestamppb.New(time.Now().Add(time.Hour))
	past := timestamppb.New(time.Now().Add(-time.Hour))

	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "vertex-duplicate", Expiration: future},
		{Key: "vertex-duplicate", Expiration: past},
	}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if _, err := svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "put", Head: "duplicate", Weight: 1, Expiration: future},
		{Tail: "put", Head: "duplicate", Weight: 1, Expiration: past},
	}}); err != nil {
		t.Fatalf("PutEdges: %v", err)
	}
	add, err := svc.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "add-0", Head: "head", Weight: 1, Expiration: future},
		nil,
		{Tail: "add-2", Head: "head", Weight: 2, Expiration: future},
	}})
	if err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	if add.GetWritten() != 2 || !slices.Equal(add.GetEffectiveWeights(), []float32{1, 0, 2}) {
		t.Fatalf("nil-slot Add response = %+v", add)
	}
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"absent", "absent"}}); err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if _, err := svc.DeleteEdges(ctx, &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "missing", Head: "edge"}, {Tail: "missing", Head: "edge"},
	}}); err != nil {
		t.Fatalf("DeleteEdges: %v", err)
	}

	for seq, want := range []struct {
		kind     string
		indexes  []uint32
		putKinds []graphPutEffectKind
	}{
		{"put", []uint32{0, 1}, []graphPutEffectKind{graphPutEffectLive, graphPutEffectBarrier}},
		{"put", []uint32{0, 1}, []graphPutEffectKind{graphPutEffectLive, graphPutEffectBarrier}},
		{"add", []uint32{0, 2}, nil},
		{"delete", []uint32{0, 1}, nil},
		{"delete", []uint32{0, 1}, nil},
	} {
		entry := mustMutationLogEntry(t, log, uint64(seq+1))
		var got []uint32
		switch effect := entry.Op.(type) {
		case *graphPutEffectEnvelope:
			if want.kind != "put" {
				t.Fatalf("entry %d = %T, want %s effect", seq+1, entry.Op, want.kind)
			}
			for _, accepted := range effect.Accepted {
				got = append(got, accepted.Index)
			}
			kinds := make([]graphPutEffectKind, len(effect.Accepted))
			for i, accepted := range effect.Accepted {
				kinds[i] = accepted.Kind
			}
			if !slices.Equal(kinds, want.putKinds) {
				t.Fatalf("entry %d Put effect kinds = %v, want %v", seq+1, kinds, want.putKinds)
			}
		case *graphAddEffectEnvelope:
			if want.kind != "add" {
				t.Fatalf("entry %d = %T, want %s effect", seq+1, entry.Op, want.kind)
			}
			got = effect.AcceptedIndexes
		case *graphDeleteEffectEnvelope:
			if want.kind != "delete" {
				t.Fatalf("entry %d = %T, want %s effect", seq+1, entry.Op, want.kind)
			}
			got = effect.AcceptedIndexes
		default:
			t.Fatalf("entry %d = %T, want effect-complete envelope", seq+1, entry.Op)
		}
		if !slices.Equal(got, want.indexes) {
			t.Fatalf("entry %d accepted indexes = %v, want %v", seq+1, got, want.indexes)
		}
	}

	var addZero, addTwo bool
	for _, edge := range cache.SnapshotEdges() {
		if len(edge.Contributions) != 1 {
			continue
		}
		switch edge.Tail {
		case "add-0":
			addZero = edge.Contributions[0].ContribID == contribIDFor(origin[:], 3, 0)
		case "add-2":
			addTwo = edge.Contributions[0].ContribID == contribIDFor(origin[:], 3, 2)
		}
	}
	if !addZero || !addTwo {
		t.Fatal("local Add nil-slot synthesis did not retain original wire indexes")
	}
}

func TestProductionGraphPublicationStrictRestartWithoutTombstones(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publication.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32, WAL: wal})
	local := bytes16("local-restart")
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewExecutor(cache).WithReplication(log, hlc.New(local, hlc.Options{}), nil)
	ctx := context.Background()
	future := timestamppb.New(time.Now().Add(time.Hour))

	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "keep", Expiration: future}, {Key: "remove", Expiration: future},
	}}); err != nil {
		t.Fatalf("local Put: %v", err)
	}
	if _, err := svc.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{{
		Tail: "local-add", Head: "head", Weight: 2, Expiration: future,
	}}}); err != nil {
		t.Fatalf("local Add: %v", err)
	}
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"remove"}}); err != nil {
		t.Fatalf("local Delete: %v", err)
	}

	remote := bytes16("remote-restart")
	remoteOps := []*pb.MutationOp{
		{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{
			Edge: &pb.Edge{Tail: "remote-put", Head: "head", Weight: 3, Expiration: future},
		}}},
		{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "remote-add", Head: "head", Weight: 4, Expiration: future},
		}}},
		{Op: &pb.MutationOp_DeleteEdge{DeleteEdge: &pb.DeleteEdgeRequest{Tail: "remote-put", Head: "head"}}},
	}
	for i, op := range remoteOps {
		seq := uint64(i + 1)
		if err := svc.ApplyMutation(ctx, &pb.Mutation{
			Origin: remote[:], Seq: seq,
			Hlc: &pb.HLCTimestamp{WallNs: time.Now().UnixNano() + int64(seq), NodeId: remote[:]},
			Op:  op,
		}); err != nil {
			t.Fatalf("remote mutation %d: %v", seq, err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	var rows int
	if err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		rows++
		switch entry.Op.(type) {
		case *graphPutEffectEnvelope, *graphAddEffectEnvelope, *graphDeleteEffectEnvelope:
			return nil
		default:
			return errors.New("production graph publication wrote a raw WAL row")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if rows != 6 {
		t.Fatalf("WAL rows = %d, want 6", rows)
	}

	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{1}, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
	}
	candidate, err := stageEffectCompleteReceiptWALCandidate(
		lease, config, time.Now(), mutationlog.Options{Capacity: 32}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil },
	)
	if err != nil {
		t.Fatalf("strict restart: %v", err)
	}
	if last, ok := candidate.log.LastSeq(); !ok || last != 6 {
		t.Fatalf("strict restart log frontier = %d, %v", last, ok)
	}
	if got := candidate.origins.LocalSeq(local); got != 3 {
		t.Fatalf("strict restart local origin = %d, want 3", got)
	}
	if got := candidate.origins.LocalSeq(remote); got != 3 {
		t.Fatalf("strict restart remote origin = %d, want 3", got)
	}
	if _, ok := candidate.graph.GetVertex("keep"); !ok {
		t.Fatal("strict restart lost accepted local Put")
	}
	if _, ok := candidate.graph.GetVertex("remove"); ok {
		t.Fatal("strict restart lost local physical Delete")
	}
	if weight, ok := candidate.graph.GetWeight("local-add", "head"); !ok || weight != 2 {
		t.Fatalf("strict restart local Add = %v, %v", weight, ok)
	}
	if _, ok := candidate.graph.GetWeight("remote-put", "head"); ok {
		t.Fatal("strict restart lost remote physical Delete")
	}
	if weight, ok := candidate.graph.GetWeight("remote-add", "head"); !ok || weight != 4 {
		t.Fatalf("strict restart remote Add = %v, %v", weight, ok)
	}
}

func TestPublishRemoteMutation_BoundedFutureAndConflictingDuplicate(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewExecutor(cache)
	origin := bytes16("remote")
	makeMutation := func(seq uint64, key string) *pb.Mutation {
		return &pb.Mutation{Seq: seq, Origin: origin[:], Hlc: newHLC(int64(seq), origin), Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}},
		}}}
	}
	far := makeMutation(maxPendingSeqGap+1, "far")
	if code := connect.CodeOf(svc.ApplyMutation(context.Background(), far)); code != connect.CodeResourceExhausted {
		t.Fatalf("unbounded gap code = %v, want ResourceExhausted", code)
	}
	if got := svc.pendingCount; got != 0 {
		t.Fatalf("rejected gap retained %d pending entries", got)
	}
	if _, ok := cache.GetVertex("far"); ok {
		t.Fatal("rejected future entry changed graph")
	}
	buffered := makeMutation(2, "one")
	if err := svc.ApplyMutation(context.Background(), buffered); err != nil {
		t.Fatal(err)
	}
	buffered.GetOp().GetPutVertex().Vertex.Key = "caller-mutated"
	if code := connect.CodeOf(svc.ApplyMutation(context.Background(), makeMutation(2, "conflict"))); code != connect.CodeFailedPrecondition {
		t.Fatalf("conflicting duplicate code = %v, want FailedPrecondition", code)
	}
	if err := svc.ApplyMutation(context.Background(), makeMutation(1, "first")); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.GetVertex("one"); !ok {
		t.Fatal("original buffered mutation did not commit")
	}
	if _, ok := cache.GetVertex("conflict"); ok {
		t.Fatal("conflicting duplicate changed graph")
	}
	if _, ok := cache.GetVertex("caller-mutated"); ok {
		t.Fatal("queued mutation retained caller-owned mutable payload")
	}
}

func TestPublishRemoteMutation_GlobalPendingBudgetRejectsBeforeApply(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewExecutor(cache)
	origin := bytes16("budget")
	m := &pb.Mutation{Seq: 2, Origin: origin[:], Hlc: newHLC(2, origin), Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
		PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "budget-key"}},
	}}}
	for _, budget := range []struct {
		name  string
		count int
		bytes int
	}{
		{"count", maxPendingMutations, 0},
		{"bytes", 0, maxPendingBytes - 1},
	} {
		t.Run(budget.name, func(t *testing.T) {
			svc.pendingCount, svc.pendingBytes = budget.count, budget.bytes
			if code := connect.CodeOf(svc.ApplyMutation(context.Background(), m)); code != connect.CodeResourceExhausted {
				t.Fatalf("pending budget code = %v, want ResourceExhausted", code)
			}
			if len(svc.pendingMutations) != 0 {
				t.Fatal("rejected mutation allocated a queue")
			}
			if _, ok := cache.GetVertex("budget-key"); ok {
				t.Fatal("rejected mutation changed graph")
			}
		})
	}
}
