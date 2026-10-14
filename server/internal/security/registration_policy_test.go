package security

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistrationPolicyBoundsAndSelection(t *testing.T) {
	valid := registrationImage().RegistrationPolicy
	if !valid.valid() || !valid.permits(GoogleIssuer, "android") || valid.permits(GoogleIssuer+"/", "android") || valid.permits(GoogleIssuer, "ios") || (*RegistrationPolicy)(nil).permits(GoogleIssuer, "android") {
		t.Fatal("registration profile widened")
	}
	for _, tc := range []struct {
		name   string
		change func(*RegistrationPolicy)
	}{
		{"version", func(p *RegistrationPolicy) { p.Version++ }},
		{"Role", func(p *RegistrationPolicy) { p.RoleID = "security_admin" }},
		{"empty allowlist", func(p *RegistrationPolicy) { p.Profiles = nil }},
		{"too many", func(p *RegistrationPolicy) { p.Profiles = make([]RegistrationProfile, 33) }},
		{"duplicate", func(p *RegistrationPolicy) { p.Profiles = append(p.Profiles, p.Profiles[0]) }},
		{"order", func(p *RegistrationPolicy) {
			p.Profiles = []RegistrationProfile{{GoogleIssuer, "ios"}, {GoogleIssuer, "android"}}
		}},
		{"profile bounds", func(p *RegistrationPolicy) { p.Profiles[0].ClientProfileID = strings.Repeat("a", 65) }},
		{"issuer syntax", func(p *RegistrationPolicy) { p.Profiles[0].Issuer = "http://accounts.google.com" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid.clone()
			tc.change(candidate)
			if candidate.valid() {
				t.Fatal("invalid policy accepted")
			}
		})
	}
	closed := valid.clone()
	closed.Enabled = false
	if !registrationPolicyExpands(nil, valid) || !registrationPolicyExpands(closed, valid) || registrationPolicyExpands(valid, closed) || registrationPolicyExpands(valid, nil) || registrationPolicyExpands(valid, valid.clone()) {
		t.Fatal("trust expansion misclassified")
	}
	expanded := valid.clone()
	expanded.Profiles = append(expanded.Profiles, RegistrationProfile{GoogleIssuer, "ios"})
	if !registrationPolicyExpands(valid, expanded) || registrationPolicyExpands(expanded, valid) {
		t.Fatal("allowlist set inclusion")
	}
	valid.Profiles[0].ClientProfileID = "modified"
	if closed.Profiles[0].ClientProfileID != "android" {
		t.Fatal("policy retained mutable input")
	}
}

func TestRegistrationPolicyImageAndLegacyCanonical(t *testing.T) {
	image := registrationImage()
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeImage(snapshot.image, DefaultPolicyLimits())
	if err != nil || !decoded.Image().RegistrationPolicy.permits(GoogleIssuer, "android") {
		t.Fatal("registration policy lost on decode", err)
	}
	image.RegistrationPolicy.Profiles[0].ClientProfileID = "unknown"
	if _, err := CompileImage(image, DefaultPolicyLimits()); err == nil {
		t.Fatal("unknown native profile configured")
	}
	legacy := testImage()
	encoded, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(encoded), "registration_policy") || strings.Contains(string(encoded), "user_uid") {
		t.Fatal("absent registration fields changed legacy encoding", err)
	}
	change := Change{Kind: PutPrincipal, Identity: &legacy.Principals[0].Identity, State: Active, RegistrationPolicy: registrationImage().RegistrationPolicy}
	if change.validate() == nil {
		t.Fatal("registration field smuggled into ordinary change")
	}
}

func TestRegistrationPolicyInitialRoleContract(t *testing.T) {
	base := PersonalUserRole()
	if len(base.Rules) != 7 || !validInitialRegistrationRole(base) {
		t.Fatal("default personal grants changed")
	}
	for _, tc := range []struct {
		name string
		rule PermissionRule
	}{
		{"global", globalRule(Allow, SecurityManage)},
		{"literal all", dataRule(Allow, VertexRead, "")},
		{"export", func() PermissionRule { r := PersonalUserRole().Rules[0]; r.Action = Export; return r }()},
		{"public root", func() PermissionRule {
			r := PersonalUserRole().Rules[0]
			r.PrincipalUIDPrefix.LiteralBefore = "public:users:"
			return r
		}()},
		{"different suffix", func() PermissionRule {
			r := PersonalUserRole().Rules[0]
			r.PrincipalUIDPrefix.LiteralAfter = ":profile:"
			return r
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role := PersonalUserRole()
			role.Rules = append(role.Rules, tc.rule)
			if validInitialRegistrationRole(role) {
				t.Fatal("unsafe first assignment")
			}
		})
	}
	base.Rules = append(base.Rules, dataRule(Deny, VertexWrite, "users:"))
	if !validInitialRegistrationRole(base) {
		t.Fatal("ordinary Deny prevented restrictive policy")
	}
}

func TestRegistrationPolicyS1TrustExpansion(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after *RegistrationPolicy
		expand        bool
	}{
		{"open", nil, registrationImage().RegistrationPolicy, true},
		{"close", registrationImage().RegistrationPolicy, &RegistrationPolicy{Version: 1, RoleID: PersonalUserRoleID}, false},
		{"unchanged", registrationImage().RegistrationPolicy, registrationImage().RegistrationPolicy, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := registrationImage()
			image.RegistrationPolicy = tc.before
			state := s1Fixture(t, image)
			change := Change{Kind: PutRegistrationPolicy, RegistrationPolicy: tc.after}
			assessment, err := AssessS1(state.projection, s1Operation(t, state.projection, testIdentity(), s1Changes(change)))
			if err != nil || assessment.NeedsPurpose != tc.expand || assessment.ProvenNonexpanding == tc.expand {
				t.Fatal("policy classification", assessment, err)
			}
			for id, epoch := range state.projection.lineage {
				if assessment.Next.lineage[id] != epoch {
					t.Fatal("policy change revoked ordinary sessions")
				}
			}
			if assessment.Next.Image().Issuers[1].ConfigRevision != image.Issuers[1].ConfigRevision {
				t.Fatal("policy changed issuer trust")
			}
		})
	}
}
