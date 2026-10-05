package security

import (
	"testing"
	"time"
)

func s1Image() Image {
	image := testImage()
	image.BootstrapRevision = 0
	image.Issuers[0].HumanSubjectNamespaceQualified = true
	image.Principals[0].Assignments[0].EnvOwned = false
	for i := range image.Roles {
		for j := range image.Roles[i].Rules {
			image.Roles[i].Rules[j].ID = "rule"
		}
	}
	image.Principals = append(image.Principals, Principal{Identity: s1Bob(), State: Active, HumanIssuerConfigRevision: 1})
	return image
}
func s1Bob() Identity { id := testIdentity(); id.Subject = "bob"; return id }
func s1Fixture(t *testing.T, image Image) *S1ApplyState {
	t.Helper()
	p, err := NewS1Projection(image, DefaultPolicyLimits(), [32]byte{1}, [32]byte{2}, [32]byte{3}, [16]byte{4})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewS1ApplyState(p, [32]byte{5}, S1Capacity{LedgerEntries: 100, RestrictiveEntries: 10, ImageBytes: MaxImageBytes, RestrictiveImageBytes: 1 << 20}, S1Retention{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func s1Operation(t *testing.T, p *S1Projection, actor Identity, c S1Command) OperationIdentity {
	t.Helper()
	o, err := NewS1Operation(actor, p.Cut(), c, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func s1Changes(changes ...Change) S1Command { return S1Command{Kind: S1Management, Changes: changes} }
func s1ReaderRole() Change {
	return Change{Kind: PutRole, Role: &Role{ID: "new_reader", Rules: []PermissionRule{{ID: "read", Effect: Allow, Action: VertexRead, Resource: DataResource, Prefix: new(string)}}}}
}
func s1Seal(p *S1Projection, o OperationIdentity, nonce byte, purpose bool) *S1Handoff {
	// Fixture stands for verified historical evidence, NOT a cryptographic
	// verifier, production gate, origin durability or qualified clock proof.
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	h := &S1Handoff{id: FullChangeID{S1Version, p.cut.Domain, p.cut.Cohort, 1, [16]byte{nonce}}, operation: o, origin: [32]byte{7}, serial: 1, authorization: &s1VerifiedAuthorization{authentication: Authentication{Provenance: RFC9068Bearer, Class: EndUser, IssuerConfigRevision: 1}, lineage: p.lineage[o.actor], credentialEvidence: [32]byte{8}, consume: now, credentialDeadline: now.Add(time.Minute)}}
	if purpose {
		h.authorization.purposeEvidence = [32]byte{9}
		h.authorization.purposeBinding = s1PurposeBinding(h.id, o)
		h.authorization.purposeDeadline = now.Add(time.Minute)
	}
	return h
}
func s1Next(s *S1ApplyState, h *S1Handoff) S1CertifiedNext {
	value := s1Digest("noop", struct{ Domain, Cohort [32]byte }{s.projection.cut.Domain, s.projection.cut.Cohort})
	if h != nil {
		value = h.digest()
	}
	return S1CertifiedNext{certificate: &s1PrefixCertificate{commit: CommitRef{S1Version, s.projection.cut.Domain, s.projection.cut.Cohort, s.membership, s.slot + 1, value}, previous: s.prefix, witness: [32]byte{10}}, handoff: h}
}
func s1Apply(t *testing.T, s *S1ApplyState, h *S1Handoff) S1ApplyResult {
	t.Helper()
	result, err := ApplyS1(s, s1Next(s, h))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
