package security

import (
	"slices"

	"github.com/anaregdesign/lantern/server/internal/authz"
)

const RegistrationPolicyVersion = 2
const PersonalUserRoleID = "personal_user"
const MaxRegistrationProfiles = 32
const NativeRegistrationProfile = "native"
const BrowserRegistrationProfile = "browser"
const BrowserRegistrationProfileID = "browser"

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
	Kind            string `json:"kind"`
}

func validRegistrationProfileKind(kind string) bool {
	return kind == NativeRegistrationProfile || kind == BrowserRegistrationProfile
}

func registrationProfileExists(issuer Issuer, kind, id string) bool {
	switch kind {
	case BrowserRegistrationProfile:
		return id == BrowserRegistrationProfileID && issuer.BrowserEnabled()
	case NativeRegistrationProfile:
		_, exists := issuer.NativeClient(id)
		return exists
	default:
		return false
	}
}

func (p *RegistrationPolicy) valid() bool {
	if p == nil {
		return true
	}
	if p.Version != RegistrationPolicyVersion || p.RoleID != PersonalUserRoleID || len(p.Profiles) > MaxRegistrationProfiles || p.Enabled && len(p.Profiles) == 0 {
		return false
	}
	for n, profile := range p.Profiles {
		if !validIssuerURL(profile.Issuer) || !validRoleID(profile.ClientProfileID) || !validRegistrationProfileKind(profile.Kind) || profile.Kind == BrowserRegistrationProfile && profile.ClientProfileID != BrowserRegistrationProfileID {
			return false
		}
		if n > 0 {
			prior := p.Profiles[n-1]
			if prior.Issuer > profile.Issuer || prior.Issuer == profile.Issuer && (prior.Kind > profile.Kind || prior.Kind == profile.Kind && prior.ClientProfileID >= profile.ClientProfileID) {
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
	return p.permitsKind(issuer, NativeRegistrationProfile, profile)
}

func (p *RegistrationPolicy) permitsKind(issuer, kind, profile string) bool {
	if p == nil || !p.Enabled {
		return false
	}
	return slices.ContainsFunc(p.Profiles, func(allowed RegistrationProfile) bool {
		return allowed.Issuer == issuer && allowed.Kind == kind && allowed.ClientProfileID == profile
	})
}

func registrationPolicyExpands(before, after *RegistrationPolicy) bool {
	if after == nil || !after.Enabled {
		return false
	}
	for _, profile := range after.Profiles {
		if !before.permitsKind(profile.Issuer, profile.Kind, profile.ClientProfileID) {
			return true
		}
	}
	return false
}

// PersonalUserRole is an explicit fresh-provisioning template. Ordinary
// startup and enrollment never insert or repair this Role.
func PersonalUserRole() Role {
	role := Role{ID: PersonalUserRoleID, Name: "Personal user"}
	for _, permission := range []Permission{Read, Write} {
		role.Rules = append(role.Rules, PermissionRule{ID: string(permission), Permission: permission, Resource: DataResource, PrincipalUIDPrefix: &authz.PrincipalUIDPrefix{LiteralBefore: "users:", LiteralAfter: ":"}})
	}
	return role
}

// Initial enrollment may assign only a subset of the trusted-UID template.
// No literal root or nondata resource can become an implicit joining grant.
func validInitialRegistrationRole(role Role) bool {
	if role.ID != PersonalUserRoleID {
		return false
	}
	for _, rule := range role.Rules {
		p := rule.PrincipalUIDPrefix
		if (rule.Permission != Read && rule.Permission != Write) || rule.Resource != DataResource || rule.Prefix != nil || p == nil || p.LiteralBefore != "users:" || p.LiteralAfter != ":" {
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
		if !known || !registrationProfileExists(issuer, profile.Kind, profile.ClientProfileID) {
			return ErrInvalidImage
		}
	}
	return nil
}
