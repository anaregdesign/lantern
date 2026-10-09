package service

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func EncodeCurrentProfile(p security.CurrentProfile) *pb.CurrentAuthorityProfile {
	return &pb.CurrentAuthorityProfile{Version: p.Version, Domain: p.Domain[:], Cohort: p.Cohort[:], Generation: p.Generation[:], Protocol: p.Protocol[:], TimeProfile: p.Time[:], Membership: p.Membership[:], Configuration: p.Configuration[:]}
}

func decodeCurrentProfile(p *pb.CurrentAuthorityProfile) (security.CurrentProfile, error) {
	if p == nil || strictSecurityMessage(p) != nil || p.Version != security.CurrentPublicVersion || len(p.Domain) != 32 || len(p.Cohort) != 32 || len(p.Generation) != 16 || len(p.Protocol) != 32 || len(p.TimeProfile) != 32 || len(p.Membership) != 32 || len(p.Configuration) != 32 {
		return security.CurrentProfile{}, security.ErrS1Contract
	}
	return security.CurrentProfile{Version: p.Version, Domain: [32]byte(p.Domain), Cohort: [32]byte(p.Cohort), Generation: [16]byte(p.Generation), Protocol: [32]byte(p.Protocol), Time: [32]byte(p.TimeProfile), Membership: [32]byte(p.Membership), Configuration: [32]byte(p.Configuration)}, nil
}

func DecodeCurrentProfile(p *pb.CurrentAuthorityProfile) (security.CurrentProfile, error) {
	return decodeCurrentProfile(p)
}

func EncodeCurrentSessionReview(r security.CurrentReview) (*pb.CurrentSessionRevocationReview, error) {
	c, err := r.Command()
	if err != nil || c.Kind != security.S1RevokeSession {
		return nil, security.ErrS1Contract
	}
	i := r.Operation.Digest()
	return &pb.CurrentSessionRevocationReview{Profile: EncodeCurrentProfile(r.Profile), ExpectedCut: encodeCurrentCut(r.Operation.Reviewed()), ChangeId: encodeCurrentID(r.ID), Actor: encodeSecurityIdentity(r.Operation.Actor()), IntentDigest: i[:], SessionDigest: c.SessionDigest, SessionLineage: c.SessionLineage}, nil
}

func DecodeCurrentSessionReview(owner *security.CurrentAuthority, r *pb.CurrentSessionRevocationReview) (security.CurrentReview, error) {
	if owner == nil || r == nil || strictSecurityMessage(r) != nil || len(r.IntentDigest) != 32 {
		return security.CurrentReview{}, security.ErrS1Contract
	}
	p, err := decodeCurrentProfile(r.Profile)
	if err != nil {
		return security.CurrentReview{}, err
	}
	c, err := decodeCurrentCut(r.ExpectedCut)
	if err != nil {
		return security.CurrentReview{}, err
	}
	id, err := decodeCurrentID(r.ChangeId)
	if err != nil {
		return security.CurrentReview{}, err
	}
	a, err := decodeSecurityIdentity(r.Actor)
	if err != nil {
		return security.CurrentReview{}, err
	}
	return owner.DecodeReview(p, id, a, c, security.S1Command{Kind: security.S1RevokeSession, SessionDigest: r.SessionDigest, SessionLineage: r.SessionLineage}, [32]byte(r.IntentDigest))
}

func encodeCurrentCut(c security.SemanticCut) *pb.CurrentSemanticCut {
	return &pb.CurrentSemanticCut{Version: uint32(c.Version), Domain: c.Domain[:], Cohort: c.Cohort[:], Generation: c.Generation[:], Sequence: c.Sequence, Previous: c.Previous[:], Projection: c.Projection[:], Frontier: c.Frontier[:], Fences: c.Fences[:], Policy: c.Policy[:]}
}

func decodeCurrentCut(c *pb.CurrentSemanticCut) (security.SemanticCut, error) {
	if c == nil || strictSecurityMessage(c) != nil || c.Version != uint32(security.S1Version) || len(c.Domain) != 32 || len(c.Cohort) != 32 || len(c.Generation) != 16 || c.Sequence == 0 || len(c.Previous) != 32 || len(c.Projection) != 32 || len(c.Frontier) != 32 || len(c.Fences) != 32 || len(c.Policy) != 32 {
		return security.SemanticCut{}, security.ErrS1Contract
	}
	return security.SemanticCut{Version: uint16(c.Version), Domain: [32]byte(c.Domain), Cohort: [32]byte(c.Cohort), Generation: [16]byte(c.Generation), Sequence: c.Sequence, Previous: [32]byte(c.Previous), Projection: [32]byte(c.Projection), Frontier: [32]byte(c.Frontier), Fences: [32]byte(c.Fences), Policy: [32]byte(c.Policy)}, nil
}

func encodeCurrentID(id security.FullChangeID) *pb.CurrentSecurityChangeID {
	return &pb.CurrentSecurityChangeID{Version: uint32(id.Version), Domain: id.Domain[:], Cohort: id.Cohort[:], Namespace: id.Namespace, Nonce: id.Nonce[:]}
}

func decodeCurrentID(id *pb.CurrentSecurityChangeID) (security.FullChangeID, error) {
	if id == nil || strictSecurityMessage(id) != nil || id.Version != uint32(security.S1Version) || len(id.Domain) != 32 || len(id.Cohort) != 32 || len(id.Nonce) != 16 || id.Namespace == 0 {
		return security.FullChangeID{}, security.ErrS1Contract
	}
	return security.FullChangeID{Version: uint16(id.Version), Domain: [32]byte(id.Domain), Cohort: [32]byte(id.Cohort), Namespace: id.Namespace, Nonce: [16]byte(id.Nonce)}, nil
}

// Current versions never forge legacy revision/digest/generation fields.
func SecurityVersionForAdmission(a *security.Admission) *pb.SecurityVersion {
	if p, current := a.CurrentProfile(); current {
		c, _ := a.CurrentCut()
		binding := a.ScopeBinding()
		return &pb.SecurityVersion{CurrentProfile: EncodeCurrentProfile(p), CurrentCut: encodeCurrentCut(c), AdmissionBinding: binding[:]}
	}
	return securityVersion(a.Revision())
}

func decodeCurrentCommand(r *pb.CurrentSecurityReview) (security.CurrentProfile, security.SemanticCut, security.S1Command, error) {
	if r == nil || strictSecurityMessage(r) != nil || len(r.Changes) == 0 || len(r.Changes) > security.MaxTransactionChanges {
		return security.CurrentProfile{}, security.SemanticCut{}, security.S1Command{}, security.ErrS1Contract
	}
	p, err := decodeCurrentProfile(r.Profile)
	if err != nil {
		return p, security.SemanticCut{}, security.S1Command{}, err
	}
	c, err := decodeCurrentCut(r.ExpectedCut)
	if err != nil {
		return p, c, security.S1Command{}, err
	}
	command := security.S1Command{Kind: security.S1Management, Changes: make([]security.Change, len(r.Changes))}
	for i, change := range r.Changes {
		command.Changes[i], err = decodeSecurityChange(change)
		if err != nil {
			return p, c, security.S1Command{}, err
		}
	}
	return p, c, command, nil
}

func (h *SecurityConnectHandler) decodeCurrentReview(r *pb.CurrentSecurityReview) (security.CurrentReview, security.S1Command, error) {
	p, c, command, err := decodeCurrentCommand(r)
	if err != nil {
		return security.CurrentReview{}, command, err
	}
	id, err := decodeCurrentID(r.ChangeId)
	if err != nil {
		return security.CurrentReview{}, command, err
	}
	actor, err := decodeSecurityIdentity(r.Actor)
	if err != nil || len(r.IntentDigest) != 32 {
		return security.CurrentReview{}, command, security.ErrS1Contract
	}
	review, err := h.current.DecodeReview(p, id, actor, c, command, [32]byte(r.IntentDigest))
	return review, command, err
}

func currentDisposition(d security.S1Disposition) pb.CurrentSecurityDisposition {
	switch d {
	case security.S1Applied:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED
	case security.S1RejectedCAS:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_REJECTED_CAS
	case security.S1RejectedAdmin:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_REJECTED_ADMIN
	case security.S1RejectedAuthority:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_REJECTED_AUTHORITY
	case security.S1RejectedPurpose:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_REJECTED_PURPOSE
	case security.S1RejectedCapacity:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_REJECTED_CAPACITY
	case security.S1RejectedInvariant:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_REJECTED_INVARIANT
	default:
		return pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_UNSPECIFIED
	}
}

func EncodeCurrentResult(r security.CurrentChangeResult) *pb.CurrentSecurityChangeResult {
	p := pb.CurrentSecurityProgress_CURRENT_SECURITY_PROGRESS_UNRESOLVED
	switch r.Progress {
	case security.CurrentOriginDurable:
		p = pb.CurrentSecurityProgress_CURRENT_SECURITY_PROGRESS_ORIGIN_DURABLE
	case security.CurrentChosen:
		p = pb.CurrentSecurityProgress_CURRENT_SECURITY_PROGRESS_CHOSEN
	case security.CurrentApplied:
		p = pb.CurrentSecurityProgress_CURRENT_SECURITY_PROGRESS_APPLIED
	}
	result := &pb.CurrentSecurityChangeResult{Profile: EncodeCurrentProfile(r.Profile), ChangeId: encodeCurrentID(r.ID), IntentDigest: r.Intent[:], Progress: p, StopObservation: pb.CurrentAuthorizationStopObservation_CURRENT_AUTHORIZATION_STOP_OBSERVATION_NOT_OBSERVED}
	if r.Original != nil {
		o, c := r.Original, r.Original.Commit()
		i, h := o.Operation().Digest(), o.HandoffDigest()
		result.Original = &pb.CurrentSecurityOriginalOutcome{ChangeId: encodeCurrentID(o.ID()), IntentDigest: i[:], HandoffDigest: h[:], Commit: &pb.CurrentControlCommit{Version: uint32(c.Version), Domain: c.Domain[:], Cohort: c.Cohort[:], Membership: c.Membership[:], Configuration: c.Configuration[:], Slot: c.Slot, Value: c.Value[:]}, Disposition: currentDisposition(o.Disposition()), ObservedCut: encodeCurrentCut(o.Observed()), ResultingCut: encodeCurrentCut(o.Resulting())}
		for _, item := range o.Items() {
			result.Original.Items = append(result.Original.Items, &pb.CurrentSecurityItemOutcome{Index: uint32(item.Index), Kind: item.Kind, Disposition: currentDisposition(item.Disposition)})
		}
	}
	if r.StopObservation == security.CurrentStopWaiting {
		result.StopObservation = pb.CurrentAuthorizationStopObservation_CURRENT_AUTHORIZATION_STOP_OBSERVATION_WAITING
	}
	if r.StopObservation == security.CurrentOldCutAuthorizationsStopped {
		result.StopObservation = pb.CurrentAuthorizationStopObservation_CURRENT_AUTHORIZATION_STOP_OBSERVATION_OLD_CUT_NEW_AUTHORIZATIONS_STOPPED
	}
	return result
}
