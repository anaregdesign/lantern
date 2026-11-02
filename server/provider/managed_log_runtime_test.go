package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

type fakeLogPolicyAuthority struct {
	mu                    sync.Mutex
	projection            security.LogPolicyProjection
	owner                 *managedLogRuntime
	readError, applyError error
	beforeReconcile       func()
	afterApply            func()
}

func (f *fakeLogPolicyAuthority) CurrentLogPolicy(ctx context.Context) (security.LogPolicyProjection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return security.LogPolicyProjection{}, err
	}
	return detachedLogProjection(f.projection), f.readError
}

func (f *fakeLogPolicyAuthority) ReconcileLogPolicy(ctx context.Context) (security.LogPolicyProjection, error) {
	f.mu.Lock()
	before := f.beforeReconcile
	f.mu.Unlock()
	if before != nil {
		before()
	}
	f.mu.Lock()
	p, err, hook := detachedLogProjection(f.projection), f.applyError, f.afterApply
	f.mu.Unlock()
	if err != nil {
		return p, err
	}
	if p.Policy != nil {
		if err = f.owner.ApplyLogPolicy(*p.Policy); err != nil {
			return p, err
		}
	}
	if hook != nil {
		hook()
	}
	return f.CurrentLogPolicy(ctx)
}

func (f *fakeLogPolicyAuthority) set(p security.LogPolicyProjection) {
	f.mu.Lock()
	f.projection = detachedLogProjection(p)
	f.mu.Unlock()
}

func logProjection(sequence, generation uint64, level security.LogLevel, fenced bool) security.LogPolicyProjection {
	p := security.LogPolicyProjection{
		Member: 101, VersionFloor: fenced,
		Cut: security.SemanticCut{Version: security.S1Version, Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{3}, Sequence: sequence, Projection: [32]byte{4}, Frontier: [32]byte{5}, Fences: [32]byte{6}, Policy: [32]byte{7}},
	}
	if generation != 0 {
		p.Policy = &security.LogPolicy{Version: security.LogPolicyVersion, Generation: generation, Level: level}
	}
	if fenced {
		p.Preparation = &security.LogPolicyPreparation{
			ChangeID:     security.FullChangeID{Version: security.S1Version, Domain: p.Cut.Domain, Cohort: p.Cut.Cohort, Namespace: 1, Nonce: [16]byte{9}},
			IntentDigest: [32]byte{10}, ActorDigest: [32]byte{11},
		}
	}
	return p
}

func managedLogFixture(p security.LogPolicyProjection, explicit bool) (*managedLogRuntime, *fakeLogPolicyAuthority) {
	m := &managedLogRuntime{owner: newServingLogger(ObservabilityConfig{LogLevel: slog.LevelError, LogLevelFromEnvironment: explicit}, io.Discard), source: securitytransport.LogSettingsSourceDefault}
	if explicit {
		m.source = securitytransport.LogSettingsSourceEnvironment
	}
	f := &fakeLogPolicyAuthority{owner: m}
	f.set(p)
	m.authority = f
	return m, f
}

func TestManagedLogRuntimeAdoptionPendingRecoveryAndRollback(t *testing.T) {
	ctx := t.Context()
	p := logProjection(1, 0, "", false)
	m, f := managedLogFixture(p, true)
	retained := m.owner.logger.With("retained", true)
	if err := m.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	initial, err := m.observation(p)
	if err != nil || initial.Source != securitytransport.LogSettingsSourceEnvironment || initial.EffectiveLevel != security.LogLevelError || initial.Pending || initial.AppliedGeneration != 0 || initial.AppliedCut != (security.SemanticCut{}) {
		t.Fatalf("initial startup observation = %+v, %v", initial, err)
	}
	p = logProjection(2, 1, security.LogLevelDebug, true)
	f.set(p)
	readBeforeWorker, err := m.observation(p)
	if err != nil || !readBeforeWorker.Pending || readBeforeWorker.AppliedGeneration != 0 || m.owner.level.Level() != slog.LevelError || m.desired.Policy != nil {
		t.Fatalf("read applied desired policy or rewrote owner state: %+v, %v", readBeforeWorker, err)
	}
	f.applyError = errors.New("test installation is pending")
	if m.reconcile(ctx) == nil {
		t.Fatal("pending installation reported success")
	}
	pending, err := m.observation(p)
	if err != nil || !pending.Pending || pending.AppliedGeneration != 0 || pending.EffectiveLevel != security.LogLevelError || pending.Source != securitytransport.LogSettingsSourceEnvironment || pending.Error != securitytransport.LogSettingsErrorOwnerUnavailable {
		t.Fatalf("commit was mistaken for application: %+v, %v", pending, err)
	}
	f.applyError = nil
	if err := m.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	applied, err := m.observation(p)
	if err != nil || applied.Pending || applied.Source != securitytransport.LogSettingsSourceManaged || applied.EffectiveLevel != security.LogLevelDebug || applied.AppliedGeneration != 1 || applied.AppliedCut != p.Cut || applied.RestartRequired || !retained.Enabled(ctx, slog.LevelDebug) {
		t.Fatalf("applied observation = %+v, %v", applied, err)
	}
	f.readError = security.ErrAuthorityUnavailable
	if m.reconcile(ctx) == nil {
		t.Fatal("lost authority remained ready")
	}
	if !retained.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("authority loss replaced the last verified level")
	}
	f.readError = nil
	pending, err = m.observation(p)
	if err != nil || pending.Pending || pending.AppliedGeneration != 1 || pending.Error != securitytransport.LogSettingsErrorNone {
		t.Fatalf("fresh read undid a retained successful installation: %+v, %v", pending, err)
	}
	if err := m.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	// An unrelated security commit advances the current cut while retaining the
	// same log-policy generation. It must not erase or remint the policy.
	unchangedPolicy := logProjection(3, 1, security.LogLevelDebug, true)
	f.set(unchangedPolicy)
	if err := m.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	same, err := m.observation(unchangedPolicy)
	if err != nil || same.Pending || same.AppliedGeneration != 1 || same.AppliedCut != p.Cut {
		t.Fatalf("unchanged policy on a new security cut = %+v, %v", same, err)
	}
	for _, next := range []security.LogPolicyProjection{logProjection(4, 2, security.LogLevelError, true), logProjection(5, 3, security.LogLevelDebug, true)} {
		f.set(next)
		if err := m.reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := m.observation(next)
		if err != nil || got.Pending || got.AppliedGeneration != next.Policy.Generation || got.EffectiveLevel != next.Policy.Level {
			t.Fatalf("update/rollback = %+v, %v", got, err)
		}
	}
}

func TestManagedLogRuntimeRecoveredPolicyOverridesConflictingStartupInput(t *testing.T) {
	committed := logProjection(9, 7, security.LogLevelWarn, true)
	for _, startup := range []ObservabilityConfig{
		{LogLevel: slog.LevelDebug, LogLevelFromEnvironment: true},
		{LogLevel: slog.LevelError, LogLevelFromEnvironment: true},
		{LogLevel: slog.LevelInfo},
	} {
		m, f := managedLogFixture(committed, startup.LogLevelFromEnvironment)
		m.owner = newServingLogger(startup, io.Discard)
		// This is the same local verified restore path used before the native
		// support binding. A fresh process starts with no applied generation.
		if m.PrepareLogPolicy() == nil {
			t.Fatal("uninitialized native projection advertised runtime support")
		}
		if err := m.observeProjection(committed); err != nil {
			t.Fatal(err)
		}
		if err := m.ApplyLogPolicy(*committed.Policy); err != nil {
			t.Fatal(err)
		}
		if m.PrepareLogPolicy() != nil {
			t.Fatal("restored actual logger did not prepare")
		}
		if err := m.reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		got, err := m.observation(committed)
		if err != nil || got.Pending || got.Source != securitytransport.LogSettingsSourceManaged || got.EffectiveLevel != security.LogLevelWarn || got.AppliedGeneration != 7 {
			t.Fatalf("recovered policy lost to startup input: %+v, %v", got, err)
		}
		f.readError = errors.New("authoritative recovery is unavailable")
		if m.reconcile(t.Context()) == nil || m.owner.level.Level() != slog.LevelWarn {
			t.Fatal("unavailable verified state silently fell back to startup input")
		}
	}
}

func TestManagedLogRuntimeRetainsStrictBootstrapValidation(t *testing.T) {
	for _, strict := range []string{"true", "false"} {
		t.Run(strict, func(t *testing.T) {
			reader := envconfig.NewReader([]string{"LANTERN_LOG_LEVEL=verbose", "LANTERN_STRICT_CONFIG=" + strict, "LANTERN_CDC_ENABLED=false"})
			cfg, err := LoadConfig(reader)
			if strict == "true" {
				if cfg != nil || err == nil || !strings.Contains(err.Error(), "LANTERN_LOG_LEVEL") {
					t.Fatal("managed ownership bypassed strict bootstrap validation", err)
				}
				return
			}
			if err != nil || cfg.Observability.LogLevel != slog.LevelInfo || cfg.Observability.LogLevelFromEnvironment || len(reader.Findings()) != 1 {
				t.Fatal("nonstrict malformed input lost bootstrap fallback/diagnostic", err)
			}
			committed := logProjection(2, 1, security.LogLevelWarn, true)
			m, _ := managedLogFixture(committed, false)
			m.owner = newServingLogger(cfg.Observability, io.Discard)
			if err := m.reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := m.observation(committed)
			if err != nil || got.Pending || got.Source != securitytransport.LogSettingsSourceManaged || got.EffectiveLevel != security.LogLevelWarn {
				t.Fatalf("verified policy did not override bootstrap fallback: %+v, %v", got, err)
			}
		})
	}
}

func TestManagedLogRuntimeRejectsRegressionsAndNeverFallsBack(t *testing.T) {
	p := logProjection(4, 3, security.LogLevelWarn, true)
	m, f := managedLogFixture(p, false)
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	foreign := logProjection(5, 4, security.LogLevelError, true)
	foreign.Cut.Domain[0]++
	for _, invalid := range []security.LogPolicyProjection{
		logProjection(3, 2, security.LogLevelError, true),
		logProjection(5, 3, security.LogLevelError, true),
		logProjection(5, 0, "", true),
		logProjection(5, 4, "verbose", true),
		logProjection(5, 4, security.LogLevelError, false),
		foreign,
	} {
		f.set(invalid)
		if m.reconcile(t.Context()) == nil {
			t.Fatalf("invalid projection installed: %+v", invalid)
		}
		if _, err := m.observation(invalid); err == nil {
			t.Fatalf("invalid projection manufactured public status: %+v", invalid)
		}
		if m.owner.level.Level() != slog.LevelWarn {
			t.Fatal("invalid or missing adopted state used another level")
		}
	}
	f.set(p)
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := m.observation(p)
	if err != nil || got.AppliedGeneration != 3 || got.Source != securitytransport.LogSettingsSourceManaged || got.Pending {
		t.Fatalf("verified recovery = %+v, %v", got, err)
	}
}

func TestManagedLogRuntimeFencedButUnadoptedAndCutChanges(t *testing.T) {
	p := logProjection(1, 0, "", true)
	m, f := managedLogFixture(p, false)
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := m.observation(p)
	if err != nil || got.Source != securitytransport.LogSettingsSourceDefault || got.AppliedGeneration != 0 || got.Pending || !m.desired.VersionFloor {
		t.Fatalf("fenced but unadopted = %+v, %v", got, err)
	}
	p.Preparation.ActorDigest[0]++
	if m.desired.Preparation.ActorDigest[0] != 11 {
		t.Fatal("retained preparation aliases an input projection")
	}
	adopted := logProjection(2, 1, security.LogLevelInfo, true)
	f.set(adopted)
	f.afterApply = func() {
		f.set(logProjection(3, 2, security.LogLevelDebug, true))
	}
	if m.reconcile(t.Context()) == nil {
		t.Fatal("changed cut was acknowledged as current application")
	}
	latest, _ := f.CurrentLogPolicy(t.Context())
	got, err = m.observation(latest)
	if err != nil || !got.Pending || got.AppliedGeneration != 1 || got.EffectiveLevel != security.LogLevelInfo || got.AppliedCut != adopted.Cut {
		t.Fatalf("raced newer desired policy = %+v, %v", got, err)
	}
	f.afterApply = nil
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err = m.observation(latest)
	if err != nil || got.Pending || got.AppliedGeneration != 2 {
		t.Fatalf("newer committed generation was not applied: %+v, %v", got, err)
	}
}

func TestManagedLogRuntimeApplicationFailureIsVisible(t *testing.T) {
	p := logProjection(1, 0, "", false)
	m, f := managedLogFixture(p, false)
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	p = logProjection(2, 1, security.LogLevelDebug, true)
	f.set(p)
	logger := m.owner.logger
	m.owner.logger = nil
	if m.reconcile(t.Context()) == nil {
		t.Fatal("missing actual handler was reported applied")
	}
	got, err := m.observation(p)
	if err != nil || !got.Pending || got.Error != securitytransport.LogSettingsErrorApplyFailed || got.AppliedGeneration != 0 || got.EffectiveLevel != security.LogLevelError {
		t.Fatalf("application failure = %+v, %v", got, err)
	}
	m.owner.logger = logger
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.owner.applyLevel(slog.LevelError)
	got, err = m.observation(p)
	if err != nil || !got.Pending || got.Error != securitytransport.LogSettingsErrorApplyFailed || got.AppliedGeneration != 1 || got.EffectiveLevel != security.LogLevelError {
		t.Fatalf("untracked logger change was mistaken for applied policy: %+v, %v", got, err)
	}
	if err := m.reconcile(t.Context()); err != nil || m.owner.level.Level() != slog.LevelDebug {
		t.Fatal("verified policy did not repair its local installation", err)
	}
}

func TestManagedLogRuntimeRetriesNewerCommitBeforeInstallation(t *testing.T) {
	p := logProjection(1, 0, "", false)
	m, f := managedLogFixture(p, true)
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.set(logProjection(2, 1, security.LogLevelWarn, true))
	latest := logProjection(3, 2, security.LogLevelDebug, true)
	f.beforeReconcile = func() { f.set(latest) }
	if m.reconcile(t.Context()) == nil {
		t.Fatal("unobserved newer policy was installed using an older cut")
	}
	got, err := m.observation(latest)
	if err != nil || !got.Pending || got.AppliedGeneration != 0 || got.EffectiveLevel != security.LogLevelError || got.Error == securitytransport.LogSettingsErrorInvalidPolicy {
		t.Fatalf("new valid commit was guessed or mislabeled: %+v, %v", got, err)
	}
	f.beforeReconcile = nil
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err = m.observation(latest)
	if err != nil || got.Pending || got.AppliedGeneration != 2 || got.AppliedCut != latest.Cut {
		t.Fatalf("latest verified policy was not installed: %+v, %v", got, err)
	}
}

func TestManagedLogRuntimeConcurrentSnapshots(t *testing.T) {
	p := logProjection(1, 0, "", false)
	m, f := managedLogFixture(p, true)
	if err := m.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	defer workers.Wait()
	for range 8 {
		workers.Go(func() {
			for range 100 {
				current, _ := f.CurrentLogPolicy(t.Context())
				observation, err := m.observation(current)
				if err == nil && observation.AppliedGeneration != 0 && observation.Source != securitytransport.LogSettingsSourceManaged {
					t.Error("snapshot mixed managed generation and startup source")
				}
				m.owner.logger.With("snapshot", true).Info("record")
			}
		})
	}
	for generation := uint64(1); generation <= 32; generation++ {
		p = logProjection(generation+1, generation, security.LogLevelDebug, true)
		f.set(p)
		if err := m.reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
	got, err := m.observation(p)
	if err != nil || got.Pending || got.AppliedGeneration != 32 {
		t.Fatalf("final snapshot = %+v, %v", got, err)
	}
}

func TestManagedLogRuntimeCertificationAndOFF(t *testing.T) {
	for _, runtime := range []*SecurityRuntime{nil, {}, {mode: "oidc"}, {mode: "oidc", current: &security.CurrentAuthority{}}} {
		if _, err := NewManagedLogRuntime(t.Context(), runtime, newServingLogger(ObservabilityConfig{}, io.Discard)); err == nil {
			t.Fatal("unavailable native owner produced public logging certification")
		}
	}
	runtime := &SecurityRuntime{mode: "off"}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewManagedLogRuntime(canceled, runtime, newServingLogger(ObservabilityConfig{}, io.Discard)); err == nil || runtime.managedLog != nil {
		t.Fatal("canceled construction installed a logger certification")
	}
	if _, err := NewManagedLogRuntime(t.Context(), runtime, nil); err == nil {
		t.Fatal("missing actual logger was certified")
	}
	owner := newServingLogger(ObservabilityConfig{LogLevel: slog.LevelWarn, LogLevelFromEnvironment: true}, io.Discard)
	certificate, err := NewManagedLogRuntime(t.Context(), runtime, owner)
	if err != nil || certificate.runtime != runtime || certificate.owner != owner || runtime.managedLog != certificate.adapter || owner.level.Level() != slog.LevelWarn {
		t.Fatalf("OFF startup ownership = %+v, %v", certificate, err)
	}
	if _, err := NewManagedLogRuntime(t.Context(), runtime, owner); err == nil {
		t.Fatal("runtime accepted a second owner binding")
	}
	if _, err := runtime.logSettingsObservation(logProjection(1, 0, "", false)); err == nil {
		t.Fatal("OFF exposed managed settings status")
	}
}

func TestManagedLogRuntimeRefusesIncompleteRecoveredProjection(t *testing.T) {
	for _, p := range []security.LogPolicyProjection{
		logProjection(1, 1, security.LogLevelWarn, false),
		func() security.LogPolicyProjection {
			p := logProjection(1, 0, "", true)
			p.Preparation = nil
			return p
		}(),
		func() security.LogPolicyProjection {
			p := logProjection(1, 0, "", true)
			p.VersionFloor = false
			return p
		}(),
		logProjection(1, 1, "unknown", true),
	} {
		m, _ := managedLogFixture(p, true)
		if m.observeProjection(p) == nil || m.PrepareLogPolicy() == nil || m.owner.level.Level() != slog.LevelError || m.member != 0 {
			t.Fatal("incomplete recovery produced prepared runtime support")
		}
	}
}
