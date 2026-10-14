package security

import (
	"slices"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/authz"
)

const RegistrationPolicyVersion = 1
const PersonalUserRoleID = "personal_user"
const MaxRegistrationProfiles = 32

// RegistrationPolicy controls creation of UIDs and first assignments. It is
// separate from issuer authentication and from access of registered users.
// Absence and Enabled=false both refuse new registration.
type RegistrationPolicy struct {
	Version  int                   `json:"version"`
	Enabled  bool                  `json:"enabled"`
	RoleID   string                `json:"role_id"`
	Profiles []RegistrationProfile `json:"profiles"`
}

type RegistrationProfile struct {
	Issuer          string `json:"issuer"`
	ClientProfileID string `json:"client_profile_id"`
}

func (p *RegistrationPolicy) valid() bool {
	if p == nil {
		return true
	}
	if p.Version != RegistrationPolicyVersion || p.RoleID != PersonalUserRoleID || len(p.Profiles) > MaxRegistrationProfiles || p.Enabled && len(p.Profiles) == 0 {
		return false
	}
	for n, profile := range p.Profiles {
		if !validIssuerURL(profile.Issuer) || !validRoleID(profile.ClientProfileID) {
			return false
		}
		if n > 0 {
			prior := p.Profiles[n-1]
			if prior.Issuer > profile.Issuer || prior.Issuer == profile.Issuer && prior.ClientProfileID >= profile.ClientProfileID {
				return false
			}
		}
	}
	return true
}

func ValidateRegistrationPolicyConfiguration(p *RegistrationPolicy) error {
	if p == nil || !p.valid() {
		return ErrInvalidImage
	}
	return nil
}

func (p *RegistrationPolicy) clone() *RegistrationPolicy {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Profiles = slices.Clone(p.Profiles)
	return &copy
}

func (p *RegistrationPolicy) permits(issuer, profile string) bool {
	return p != nil && p.Enabled && slices.Contains(p.Profiles, RegistrationProfile{issuer, profile})
}

func registrationPolicyExpands(before, after *RegistrationPolicy) bool {
	if after == nil || !after.Enabled {
		return false
	}
	for _, profile := range after.Profiles {
		if !before.permits(profile.Issuer, profile.ClientProfileID) {
			return true
		}
	}
	return false
}

func registrationAction(action Action) bool {
	switch action {
	case VertexRead, VertexWrite, VertexDelete, Query, CDCIdentity, CDCValue, ReceiptRead:
		return true
	default:
		return false
	}
}

// PersonalUserRole is an explicit setup template. Construction grants nothing;
// provider startup and registration never insert or repair this Role.
func PersonalUserRole() Role {
	role := Role{ID: PersonalUserRoleID, Name: "Personal user"}
	for _, action := range []Action{VertexRead, VertexWrite, VertexDelete, Query, CDCIdentity, CDCValue, ReceiptRead} {
		role.Rules = append(role.Rules, PermissionRule{ID: strings.ReplaceAll(string(action), ".", "-"), Effect: Allow, Action: action, Resource: DataResource, PrincipalUIDPrefix: &authz.PrincipalUIDPrefix{LiteralBefore: "users:", LiteralAfter: ":"}})
	}
	return role
}

// Initial enrollment may assign only the reviewed personal Role. Operators can
// restrict it further with normal Denies; arbitrary UID roots or new Allow
// capabilities cannot silently become default registration grants.
func validInitialRegistrationRole(role Role) bool {
	if role.ID != PersonalUserRoleID {
		return false
	}
	for _, rule := range role.Rules {
		if rule.Effect != Allow {
			continue
		}
		p := rule.PrincipalUIDPrefix
		if !registrationAction(rule.Action) || rule.Resource != DataResource || rule.Prefix != nil || rule.Pair != nil || p == nil || p.LiteralBefore != "users:" || p.LiteralAfter != ":" {
			return false
		}
	}
	return true
}

func validateRegistrationPolicy(p *RegistrationPolicy, snapshot *Snapshot) error {
	if !p.valid() {
		return ErrInvalidImage
	}
	if p == nil {
		return nil
	}
	for _, profile := range p.Profiles {
		issuer, known := snapshot.Issuer(profile.Issuer)
		if _, exists := issuer.NativeClient(profile.ClientProfileID); !known || !exists {
			return ErrInvalidImage
		}
	}
	return nil
}
