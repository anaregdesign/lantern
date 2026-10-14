package service

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func encodeRegistrationReview(r security.CurrentReview) (*pb.NativeRegistrationReview, error) {
	c, err := r.Command()
	if err != nil || c.Kind != security.S1RegisterPrincipal || c.Registration == nil || security.RegistrationReviewBinding(r) == [32]byte{} {
		return nil, security.ErrS1Contract
	}
	i := r.Operation.Digest()
	return &pb.NativeRegistrationReview{Profile: EncodeCurrentProfile(r.Profile), ExpectedCut: encodeCurrentCut(r.Operation.Reviewed()), ChangeId: encodeCurrentID(r.ID), Actor: encodeSecurityIdentity(r.Operation.Actor()), IntentDigest: i[:],
		UserId: c.Registration.UserUID, ClientProfileId: c.Registration.ClientProfileID, IssuerConfigRevision: c.Registration.IssuerConfigRevision}, nil
}

func decodeRegistrationReview(owner *security.CurrentAuthority, r *pb.NativeRegistrationReview) (security.CurrentReview, error) {
	if owner == nil || r == nil || strictSecurityMessage(r) != nil || len(r.IntentDigest) != 32 {
		return security.CurrentReview{}, security.ErrS1Contract
	}
	p, err := decodeCurrentProfile(r.Profile)
	if err != nil {
		return security.CurrentReview{}, err
	}
	cut, err := decodeCurrentCut(r.ExpectedCut)
	if err != nil {
		return security.CurrentReview{}, err
	}
	id, err := decodeCurrentID(r.ChangeId)
	if err != nil {
		return security.CurrentReview{}, err
	}
	actor, err := decodeSecurityIdentity(r.Actor)
	if err != nil {
		return security.CurrentReview{}, err
	}
	command := security.S1Command{Kind: security.S1RegisterPrincipal, Registration: &security.PrincipalRegistration{UserUID: r.UserId, ClientProfileID: r.ClientProfileId, IssuerConfigRevision: r.IssuerConfigRevision}}
	return owner.DecodeReview(p, id, actor, cut, command, [32]byte(r.IntentDigest))
}

func decodeRegistrationPolicy(p *pb.SecurityRegistrationPolicy) (*security.RegistrationPolicy, error) {
	if p == nil || strictSecurityMessage(p) != nil || p.Version != security.RegistrationPolicyVersion || len(p.Profiles) > security.MaxRegistrationProfiles {
		return nil, security.ErrInvalidImage
	}
	result := &security.RegistrationPolicy{Version: int(p.Version), Enabled: p.Enabled, RoleID: p.RoleId, Profiles: make([]security.RegistrationProfile, len(p.Profiles))}
	for n, profile := range p.Profiles {
		if profile == nil {
			return nil, security.ErrInvalidImage
		}
		result.Profiles[n] = security.RegistrationProfile{Issuer: profile.Issuer, ClientProfileID: profile.ClientProfileId}
	}
	if err := security.ValidateRegistrationPolicyConfiguration(result); err != nil {
		return nil, err
	}
	return result, nil
}

func encodeRegistrationPolicy(p *security.RegistrationPolicy) *pb.SecurityRegistrationPolicy {
	if p == nil {
		return nil
	}
	result := &pb.SecurityRegistrationPolicy{Version: uint32(p.Version), Enabled: p.Enabled, RoleId: p.RoleID}
	for _, profile := range p.Profiles {
		result.Profiles = append(result.Profiles, &pb.SecurityRegistrationProfile{Issuer: profile.Issuer, ClientProfileId: profile.ClientProfileID})
	}
	return result
}
