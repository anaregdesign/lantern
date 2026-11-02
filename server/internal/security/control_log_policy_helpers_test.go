package security

import (
	"bytes"
	"crypto/ed25519"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
)

// This owner has an actual resident handler and mutable level. It is a private
// unit fixture, not evidence that production provider composition is installed.
type logPolicyRuntimeTestOwner struct {
	level                slog.LevelVar
	logger               *slog.Logger
	prepareErr, applyErr error
	applied              []LogPolicy
	onApply              func() error
}

func newLogPolicyRuntimeTestOwner() *logPolicyRuntimeTestOwner {
	owner := &logPolicyRuntimeTestOwner{}
	owner.logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: &owner.level}))
	return owner
}
func (o *logPolicyRuntimeTestOwner) PrepareLogPolicy() error {
	if o.logger == nil {
		return ErrLogPolicyUnavailable
	}
	return o.prepareErr
}
func (o *logPolicyRuntimeTestOwner) ApplyLogPolicy(policy LogPolicy) error {
	if o.applyErr != nil {
		return o.applyErr
	}
	if err := ValidateLogPolicyConfiguration(&policy); err != nil {
		return err
	}
	if o.onApply != nil {
		if err := o.onApply(); err != nil {
			return err
		}
	}
	level := map[LogLevel]slog.Level{LogLevelDebug: slog.LevelDebug, LogLevelInfo: slog.LevelInfo, LogLevelWarn: slog.LevelWarn, LogLevelError: slog.LevelError}[policy.Level]
	o.level.Set(level)
	o.applied = append(o.applied, policy)
	return nil
}

// These private helpers construct codec fixtures only. They do not establish
// native durability or an installed production runtime owner.
func logPolicyCertificateFixture(t *testing.T, count int) (*s2cTestFixture, *s2cHistoricalH, CurrentProfile, []logPolicySupportAttestation, []logPolicySupportAttestation) {
	t.Helper()
	fixture, originKeys := authorityTestFixture(t, count)
	header, _ := authorityTestHeader(t, fixture)
	op := s1Operation(t, fixture.genesis.state.projection, testIdentity(), s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelInfo}}))
	header.OperationDigest = op.digest
	raw := authorityTestSealHeader(t, fixture, originKeys[1], header, op)
	original, err := verifyAuthorityHistoricalH(fixture.trust, raw)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := logPolicyProfileForOriginal(fixture.trust, original)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := newLogPolicySupportStatement(fixture.trust, profile, original)
	if err != nil {
		t.Fatal(err)
	}
	var voters, origins []logPolicySupportAttestation
	floors := make(map[uint32]s2cJournalFloor)
	for _, member := range fixture.members {
		floor := s2cJournalFloor{Scope: [32]byte{1, byte(member.ID)}, Index: 3, Chain: [32]byte{2, byte(member.ID)}}
		floors[member.ID] = floor
		proof := logPolicySupportAttestation{Role: logPolicySupportVoter, Signer: member.ID, Floor: floor}
		body, err := logPolicySupportAttestationBody(statement, proof)
		if err != nil {
			t.Fatal(err)
		}
		proof.Signature = ed25519.Sign(fixture.keys[member.ID], body)
		voters = append(voters, proof)
	}
	for _, origin := range fixture.origins {
		proof := logPolicySupportAttestation{Role: logPolicySupportOrigin, Signer: origin.ID, Floor: floors[origin.Member]}
		body, err := logPolicySupportAttestationBody(statement, proof)
		if err != nil {
			t.Fatal(err)
		}
		proof.Signature = ed25519.Sign(originKeys[origin.ID], body)
		origins = append(origins, proof)
	}
	return fixture, original, profile, voters, origins
}

func cloneLogPolicyAttestations(proofs []logPolicySupportAttestation) []logPolicySupportAttestation {
	out := append([]logPolicySupportAttestation(nil), proofs...)
	for i := range out {
		out[i].Signature = bytes.Clone(out[i].Signature)
	}
	return out
}

func authorityCertificateOriginKeys(t *testing.T, count int) map[uint32]ed25519.PrivateKey {
	t.Helper()
	_, keys := authorityTestFixture(t, count)
	return keys
}

// logPolicyRuntimeCommittedFixture uses the ordinary reviewed Apply path, real
// P/B files, private TLS and all-participant support. Only credentials/time and
// the resident logging owner are explicit deterministic unit fixtures.
func logPolicyRuntimeCommittedFixture(t *testing.T, level LogLevel) (*s3aTestNetwork, *CurrentAuthority, *logPolicyRuntimeTestOwner, *atomic.Uint64) {
	t.Helper()
	network, owners, ticks := authorityTestCompositeMembers(t, 1, 1, nil)
	authority := &CurrentAuthority{origin: owners[1]}
	owner := newLogPolicyRuntimeTestOwner()
	if err := authority.BindLogPolicyRuntime(owner); err != nil {
		t.Fatal(err)
	}
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
	review, _, err := authority.Prepare(ctx, admission, producer, authority.Profile(), cut, s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: level}}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := authority.Apply(ctx, review, producer, [32]byte{})
	if err != nil || result.Progress != CurrentApplied || result.Original == nil || result.Original.Disposition() != S1Applied {
		t.Fatal("runtime fixture policy did not commit through reviewed native Apply", result.Progress, err)
	}
	return network, authority, owner, ticks[1]
}
