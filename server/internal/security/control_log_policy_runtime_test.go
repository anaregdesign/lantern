package security

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type logPolicyRuntimeMachineProducer struct{ digest string }

func (p logPolicyRuntimeMachineProducer) VerifyCurrentCredential(context.Context, CurrentCredentialView) (CurrentCredentialFacts, error) {
	return CurrentCredentialFacts{MachineDigest: p.digest}, nil
}

func TestLogPolicyRuntimeBindingRequiresResidentOwnerOnce(t *testing.T) {
	_, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	authority := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if _, err := authority.ReconcileLogPolicy(ctx); !errors.Is(err, ErrLogPolicyUnavailable) {
		t.Fatal("absent owner appeared installed", err)
	}
	if err := authority.BindLogPolicyRuntime(nil); !errors.Is(err, ErrLogPolicyUnavailable) {
		t.Fatal("nil owner bound", err)
	}
	if err := authority.BindLogPolicyRuntime(&logPolicyRuntimeTestOwner{}); !errors.Is(err, ErrLogPolicyUnavailable) {
		t.Fatal("unconstructed owner bound", err)
	}
	owner := newLogPolicyRuntimeTestOwner()
	owner.prepareErr = errors.New("resident logger unavailable")
	if err := authority.BindLogPolicyRuntime(owner); !errors.Is(err, ErrLogPolicyUnavailable) {
		t.Fatal("unprepared owner bound", err)
	}
	owner.prepareErr = nil
	if err := authority.BindLogPolicyRuntime(owner); err != nil {
		t.Fatal(err)
	}
	if owner.level.Level() != slog.LevelInfo || len(owner.applied) != 0 {
		t.Fatal("binding changed the logger or claimed policy application")
	}
	if err := authority.BindLogPolicyRuntime(newLogPolicyRuntimeTestOwner()); !errors.Is(err, ErrLogPolicyUnavailable) {
		t.Fatal("resident owner replaced", err)
	}
	if _, err := authority.ReconcileLogPolicy(ctx); err != nil || len(owner.applied) != 0 {
		t.Fatal("unadopted policy changed local logger", err)
	}
}

func TestLogPolicyRuntimeProjectionBeforeFreshRenewalAndErrors(t *testing.T) {
	_, authority, owner, ticks := logPolicyRuntimeCommittedFixture(t, LogLevelWarn)
	ctx := s3aTestContext(t)
	ticks.Add(uint64(20 * time.Second))
	if authority.Ready(ctx) {
		t.Fatal("expired fixture retained fresh quorum eligibility")
	}
	projection, err := authority.CurrentLogPolicy(ctx)
	if err != nil || projection.Policy == nil || projection.Policy.Level != LogLevelWarn || projection.Policy.Generation != 1 || projection.Member != 1 || !projection.VersionFloor {
		t.Fatal("verified local projection required fresh quorum or lost committed data", err)
	}
	projection.Policy.Level = LogLevelDebug
	again, err := authority.CurrentLogPolicy(ctx)
	if err != nil || again.Policy.Level != LogLevelWarn {
		t.Fatal("projection aliases immutable policy", err)
	}
	if _, err := authority.ReconcileLogPolicy(ctx); err != nil || len(owner.applied) != 1 || owner.logger.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("resident logger did not apply verified policy", err)
	}
	owner.prepareErr = errors.New("lost handler")
	if p, err := authority.ReconcileLogPolicy(ctx); !errors.Is(err, ErrLogPolicyUnavailable) || p.Policy == nil || p.Policy.Generation != 1 {
		t.Fatal("owner failure lost desired state or fell back", err)
	}
	owner.prepareErr = nil
	owner.applyErr = errors.New("application failed")
	if _, err := authority.ReconcileLogPolicy(ctx); !errors.Is(err, ErrLogPolicyUnavailable) || len(owner.applied) != 1 {
		t.Fatal("failed application reported success", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := authority.CurrentLogPolicy(cancelled); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("cancelled observation succeeded", err)
	}
	if _, err := (*CurrentAuthority)(nil).CurrentLogPolicy(ctx); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("missing owner supplied projection", err)
	}
}

func TestLogPolicyRuntimeAdmissionRequiresExactCut(t *testing.T) {
	_, owners, _ := authorityTestCompositeMembers(t, 1, 1, nil)
	authority := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if _, err := authority.LogPolicyForAdmission(ctx, nil); err == nil {
		t.Fatal("missing admission disclosed policy")
	}
	if err := authority.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	admission, err := authority.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := authority.LogPolicyForAdmission(ctx, admission)
	if err != nil || projection.Policy != nil {
		t.Fatal("exact admission did not read unadopted state", err)
	}
	cut, _ := admission.CurrentCut()
	review, _, err := authority.Prepare(ctx, admission, producer, authority.Profile(), cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Apply(ctx, review, producer, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.LogPolicyForAdmission(ctx, admission); err == nil {
		t.Fatal("old security cut disclosed current policy")
	}
	if err := authority.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, err := authority.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.LogPolicyForAdmission(ctx, fresh); err != nil {
		t.Fatal("fresh security cut could not read policy", err)
	}
}

func TestLogPolicyRuntimeQualifiedMachineReadCannotMutate(t *testing.T) {
	image := s1Image()
	machine := Identity{Kind: MachinePrincipal, MachineName: "settings-observer"}
	digest := strings.Repeat("b", 64)
	now := time.Now().UTC()
	image.Principals = append(image.Principals, Principal{Identity: machine, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	image.MachineCredentials = []MachineCredential{{Identity: machine, Digest: digest, CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}
	_, owners, _ := authorityTestCompositeMembers(t, 1, 1, &image)
	authority := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if err := authority.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := logPolicyRuntimeMachineProducer{digest}
	admission, err := authority.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Authentication().Class != MachineActor {
		t.Fatal("machine relabeled")
	}
	if _, err := authority.LogPolicyForAdmission(ctx, admission); err != nil {
		t.Fatal("qualified machine reference read refused", err)
	}
	cut, _ := admission.CurrentCut()
	before := authority.origin.network.kernel.p.floor
	if _, _, err := authority.Prepare(ctx, admission, producer, authority.Profile(), cut, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelDebug}})); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("machine prepared settings mutation", err)
	}
	if authority.origin.network.kernel.p.floor != before {
		t.Fatal("refused machine mutation changed durable state")
	}
}

func TestLogPolicyRuntimeReconcileRejectsConcurrentCutChange(t *testing.T) {
	_, authority, owner, _ := logPolicyRuntimeCommittedFixture(t, LogLevelInfo)
	ctx := s3aTestContext(t)
	if err := authority.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	admission, err := authority.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := admission.CurrentCut()
	review, _, err := authority.Prepare(ctx, admission, producer, authority.Profile(), cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	owner.onApply = func() error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	type result struct {
		projection LogPolicyProjection
		err        error
	}
	done := make(chan result, 1)
	go func() { p, err := authority.ReconcileLogPolicy(ctx); done <- result{p, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("logger application was not entered", ctx.Err())
	}
	if _, err := authority.Apply(ctx, review, producer, [32]byte{}); err != nil {
		kernel := authority.origin.network.kernel
		kernel.gate.Lock()
		t.Logf("apply failure state: slot=%d p_records=%d selected=%t accepted=%t chosen=%t promises=%d votes=%d pending=%d", kernel.replayState.slot, kernel.p.count, kernel.selected != nil, kernel.accepted != nil, kernel.chosen != nil, len(kernel.promises), len(kernel.votes), len(kernel.pending))
		for _, pending := range kernel.pending {
			commit := s2cLogicalCommit(kernel.trust, kernel.b.state.slot+1, pending.digest())
			_, previewErr := s2cPreviewCandidate(kernel.trust, kernel.b.state, kernel.trust.genesis.roots, kernel.config.BScope, kernel.b.receipt, pending, commit)
			t.Logf("pending candidate: log_change=%t supported_wrapper=%t preview_error=%v", hasLogPolicyChange(pending.handoff), pending.candidate != "", previewErr)
		}
		kernel.gate.Unlock()
		t.Fatal(err)
	}
	// The resident owner returns without calling back into authority. The
	// concurrent mutation is performed by this independent test actor.
	close(release)
	released = true
	select {
	case got := <-done:
		if !errors.Is(got.err, ErrAuthorityUnavailable) || got.projection.Cut == cut {
			t.Fatal("raced projection claimed coherent application", got.err)
		}
	case <-ctx.Done():
		t.Fatal("logger application did not finish", ctx.Err())
	}
}

func TestLogPolicyRuntimeRestoresVerifiedProjectionBeforeRenewal(t *testing.T) {
	network, oldAuthority, _, _ := logPolicyRuntimeCommittedFixture(t, LogLevelError)
	old := oldAuthority.origin.network
	before, err := oldAuthority.CurrentLogPolicy(s3aTestContext(t))
	if err != nil {
		t.Fatal(err)
	}
	floors, err := old.Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	config := network.configs[1]
	resumed, err := resumeS3AOwner(config, floors)
	if err != nil {
		t.Fatal("verified native reconstruction failed", err)
	}
	t.Cleanup(func() {
		if err := resumed.Close(); err != nil {
			t.Error(err)
		}
	})
	key, err := loadAuthorityOriginKey(config, filepath.Join(filepath.Dir(config.Identity.VotingKey), "origin.key"))
	if err != nil {
		t.Fatal(err)
	}
	origin, err := attachAuthorityOrigin(resumed, key, "https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	authority := &CurrentAuthority{origin: origin}
	ctx := s3aTestContext(t)
	if authority.Ready(ctx) {
		t.Fatal("restart inherited live renewal authority")
	}
	projection, err := authority.CurrentLogPolicy(ctx)
	if err != nil || projection.Policy == nil || *projection.Policy != *before.Policy || projection.Cut != before.Cut || !projection.VersionFloor {
		t.Fatal("reconstructed policy required a new quorum or lost exact ancestry", err)
	}
	if _, err := authority.ReconcileLogPolicy(ctx); !errors.Is(err, ErrLogPolicyUnavailable) {
		t.Fatal("restored data invented an installed process owner", err)
	}
	owner := newLogPolicyRuntimeTestOwner()
	if err := authority.BindLogPolicyRuntime(owner); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.ReconcileLogPolicy(ctx); err != nil || owner.level.Level() != slog.LevelError {
		t.Fatal("reconstructed desired state did not apply before fresh renewal", err)
	}
}
