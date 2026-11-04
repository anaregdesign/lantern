package security

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

// These helpers build authentic codec originals and signatures. Synthetic
// floors are not evidence of durable preparation or production runtime support.
func operationalPolicyCertificateFixture(t *testing.T, group operationalpolicy.Group, count int) (*s2cTestFixture, *s2cHistoricalH, CurrentProfile, []operationalPolicySupportAttestation, []operationalPolicySupportAttestation) {
	t.Helper()
	fixture, originKeys := authorityTestFixture(t, count)
	header, _ := authorityTestHeader(t, fixture)
	values, err := operationalpolicy.Defaults(group)
	if err != nil {
		t.Fatal(err)
	}
	change := operationalpolicy.Change{Values: values, RetentionReductionAcknowledged: group == operationalpolicy.GroupBackup}
	if group == operationalpolicy.GroupSearch {
		change.CompatibilityFingerprint = strings.Repeat("ab", 32)
	}
	op := s1Operation(t, fixture.genesis.state.projection, testIdentity(), s1Changes(Change{Kind: PutOperationalPolicy, OperationalPolicy: &change}))
	header.OperationDigest = op.digest
	raw := authorityTestSealHeader(t, fixture, originKeys[1], header, op)
	original, err := verifyAuthorityHistoricalH(fixture.trust, raw)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := operationalPolicyProfileForOriginal(fixture.trust, original)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := newOperationalPolicySupportStatement(fixture.trust, profile, original)
	if err != nil {
		t.Fatal(err)
	}
	var voters, origins []operationalPolicySupportAttestation
	floors := make(map[uint32]s2cJournalFloor)
	for _, member := range fixture.members {
		floor := s2cJournalFloor{Scope: [32]byte{1, byte(member.ID)}, Index: 3, Chain: [32]byte{2, byte(member.ID)}}
		floors[member.ID] = floor
		proof := operationalPolicySupportAttestation{Role: operationalPolicySupportVoter, Signer: member.ID, Floor: floor}
		body, err := operationalPolicySupportAttestationBody(statement, proof)
		if err != nil {
			t.Fatal(err)
		}
		proof.Signature = ed25519.Sign(fixture.keys[member.ID], body)
		voters = append(voters, proof)
	}
	for _, origin := range fixture.origins {
		proof := operationalPolicySupportAttestation{Role: operationalPolicySupportOrigin, Signer: origin.ID, Floor: floors[origin.Member]}
		body, err := operationalPolicySupportAttestationBody(statement, proof)
		if err != nil {
			t.Fatal(err)
		}
		proof.Signature = ed25519.Sign(originKeys[origin.ID], body)
		origins = append(origins, proof)
	}
	return fixture, original, profile, voters, origins
}

func cloneOperationalPolicyAttestations(proofs []operationalPolicySupportAttestation) []operationalPolicySupportAttestation {
	out := append([]operationalPolicySupportAttestation(nil), proofs...)
	for i := range out {
		out[i].Signature = bytes.Clone(out[i].Signature)
	}
	return out
}

func operationalPolicyTestCandidate(t *testing.T, group operationalpolicy.Group, count int) (*s2cTestFixture, *s2cHistoricalH, *s2cHistoricalH) {
	t.Helper()
	f, original, profile, voters, origins := operationalPolicyCertificateFixture(t, group, count)
	proof, err := encodeOperationalPolicySupportCertificate(f.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeOperationalPolicyCandidate(f.trust, original, proof)
	if err != nil {
		t.Fatal(err)
	}
	return f, original, candidate
}
