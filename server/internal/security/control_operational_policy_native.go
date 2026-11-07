package security

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"net/http"
	"sort"
	"time"
)

const operationalPolicySupportPath = "/lantern-private/control/operational-policy-v1/support"

type operationalPolicySupportResponse struct {
	Voter   operationalPolicySupportAttestation
	Origins []operationalPolicySupportAttestation
}

func operationalPolicyCandidateBound(t *s2cTrust, profile CurrentProfile, group operationalpolicy.Group, originalBytes uint64) (uint64, error) {
	proof, err := operationalPolicySupportCertificateBound(t, profile, group)
	if err != nil {
		return 0, err
	}
	total, err := s2CheckedAdd(originalBytes, proof+uint64(len(operationalPolicyCandidateMagic)+8))
	if err != nil || total > t.bounds.HistoricalBytes || s2cChosenSizeBound(t, total) == 0 {
		return 0, errS2CCapacity
	}
	return total, nil
}

// No support signature is minted until an authentic reviewed Apply H exists,
// the resident owner is installed, the whole bounded successor is representable,
// and the local minimum-version floor has crossed real WAL+tip durability.
func (o *authorityOriginOwner) supportOperationalPolicyLocked(ctx context.Context, raw []byte) (operationalPolicySupportResponse, error) {
	k := o.network.kernel
	if ctx.Err() != nil || o.closed || k.readyLocked() != nil || k.chosen != nil || !o.configuredReadyLocked() {
		return operationalPolicySupportResponse{}, ErrOperationalPolicyUnavailable
	}
	h, err := verifyAuthorityHistoricalH(k.trust, raw)
	if err != nil || !hasOperationalPolicyChange(h.handoff) || h.handoff.operation.reviewed != k.replayState.projection.cut {
		return operationalPolicySupportResponse{}, errOperationalPolicySupport
	}
	change, group, ok := operationalPolicyChangeForH(h.handoff)
	owner := k.operationalRuntimes[group]
	if !ok || owner == nil || owner.AvailableOperationalPolicy(group, operationalpolicy.Version) != nil || owner.PrepareOperationalPolicy(change) != nil {
		return operationalPolicySupportResponse{}, ErrOperationalPolicyUnavailable
	}
	profile, err := operationalPolicyProfileForOriginal(k.trust, h)
	if err != nil || profile != (&CurrentAuthority{origin: o}).Profile() {
		return operationalPolicySupportResponse{}, errOperationalPolicySupport
	}
	assessment, err := k.assessOperationalPolicyFloor(h)
	if err != nil {
		return operationalPolicySupportResponse{}, err
	}
	charge, err := authoritySuccessorCharge(k, h.handoff.id, h.handoff.operation, assessment)
	if err != nil {
		return operationalPolicySupportResponse{}, err
	}
	bound, err := operationalPolicyCandidateBound(k.trust, profile, group, uint64(len(h.raw)))
	if err != nil || bound > k.config.PendingBytes || s2cChosenSizeBound(k.trust, bound) > k.config.OutboxBytes/uint64(len(k.trust.members)) {
		return operationalPolicySupportResponse{}, errS2CCapacity
	}
	candidateCharge, err := s2cPCharge(bound)
	if err != nil {
		return operationalPolicySupportResponse{}, err
	}
	chosenCharge, err := s2cPCharge(s2cChosenSizeBound(k.trust, bound))
	if err != nil {
		return operationalPolicySupportResponse{}, err
	}
	drained, _ := s2cPCharge(0)
	protected := s2cMaxCredit(k.credit, s2cCredit{PBytes: candidateCharge + chosenCharge + drained, PRecords: 3, BBytes: charge, BRecords: 1})
	// Even an already-fenced participant checks the complete next candidate's
	// budget before endorsing another attempt. An unchosen H gains no promise
	// of future capacity beyond the existing accepted-completion credit.
	if !k.bCreditFits(protected) {
		return operationalPolicySupportResponse{}, errS2CCapacity
	}
	if k.operationalFloors[group] == nil {
		floorRaw, err := operationalPolicyFloorBytes(h)
		if err != nil {
			return operationalPolicySupportResponse{}, err
		}
		encodedFloor, err := k.encodeRecord(s2cPRecord{kind: s2cPOperationalPolicyFloor, index: k.p.count + 1, raw: string(floorRaw)})
		if err != nil {
			return operationalPolicySupportResponse{}, err
		}
		if err = k.p.checkAppend(encodedFloor, protected.PBytes, protected.PRecords); err != nil {
			return operationalPolicySupportResponse{}, err
		}
	} else if k.p.used > k.p.policy.Bytes || protected.PBytes > k.p.policy.Bytes-k.p.used || k.p.count > k.p.policy.Records || protected.PRecords > k.p.policy.Records-k.p.count {
		return operationalPolicySupportResponse{}, errS2CCapacity
	}
	start, expiry, timeErr := o.eligibilityLocked(ctx)
	if timeErr != nil {
		return operationalPolicySupportResponse{}, timeErr
	}
	now, _, sampleErr := o.network.receiver.currentLocked()
	err = sampleErr
	if sampleErr == nil {
		low, high := time.Unix(0, int64(now.utc.low)), time.Unix(0, int64(now.utc.high))
		if low.Before(start) || !high.Before(expiry) {
			return operationalPolicySupportResponse{}, ErrAuthorityUnavailable
		}
	}
	if err != nil {
		return operationalPolicySupportResponse{}, err
	}
	if err = k.ensureOperationalPolicyFloorLocked(h, protected); err != nil {
		return operationalPolicySupportResponse{}, err
	}
	statement, err := newOperationalPolicySupportStatement(k.trust, profile, h)
	if err != nil {
		return operationalPolicySupportResponse{}, err
	}
	response := operationalPolicySupportResponse{Voter: operationalPolicySupportAttestation{Role: operationalPolicySupportVoter, Signer: k.config.Member, Floor: k.operationalFloors[group].floor}, Origins: []operationalPolicySupportAttestation{}}
	body, err := operationalPolicySupportAttestationBody(statement, response.Voter)
	if err != nil {
		return operationalPolicySupportResponse{}, err
	}
	response.Voter.Signature = ed25519.Sign(k.key, body)
	if o.descriptor.ID != 0 {
		origin := operationalPolicySupportAttestation{Role: operationalPolicySupportOrigin, Signer: o.descriptor.ID, Floor: k.operationalFloors[group].floor}
		body, err = operationalPolicySupportAttestationBody(statement, origin)
		if err != nil {
			return operationalPolicySupportResponse{}, err
		}
		if len(o.key) != ed25519.PrivateKeySize {
			return operationalPolicySupportResponse{}, ErrOperationalPolicyUnavailable
		}
		origin.Signature = ed25519.Sign(o.key, body)
		response.Origins = append(response.Origins, origin)
	}
	return response, nil
}

func (o *s3aOwner) operationalPolicySupportResponse(ctx context.Context, raw []byte, check func() error) ([]byte, error) {
	if o.origin == nil || check == nil {
		return nil, ErrOperationalPolicyUnavailable
	}
	k := o.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if check() != nil {
		return nil, ErrAuthorityUnavailable
	}
	response, err := o.origin.supportOperationalPolicyLocked(ctx, raw)
	if err != nil {
		return nil, err
	}
	if check() != nil {
		return nil, ErrAuthorityUnavailable
	}
	return json.Marshal(response)
}

func (o *authorityOriginOwner) collectOperationalPolicySupport(ctx context.Context, h *s2cHistoricalH) (*s2cHistoricalH, error) {
	n, k := o.network, o.network.kernel
	profile, err := operationalPolicyProfileForOriginal(k.trust, h)
	if err != nil {
		return nil, err
	}
	statement, err := newOperationalPolicySupportStatement(k.trust, profile, h)
	if err != nil {
		return nil, err
	}
	voters := make([]operationalPolicySupportAttestation, 0, len(k.trust.members))
	origins := make([]operationalPolicySupportAttestation, 0, len(k.trust.origins))
	for _, member := range k.trust.members {
		var response operationalPolicySupportResponse
		if member.ID == k.config.Member {
			k.gate.Lock()
			response, err = o.supportOperationalPolicyLocked(ctx, []byte(h.raw))
			k.gate.Unlock()
		} else {
			var raw []byte
			var status int
			raw, status, _, err = n.requestAdmitted(ctx, member.ID, operationalPolicySupportPath, []byte(h.raw), operationalPolicySupportCertificateMax)
			if err == nil && status != http.StatusOK {
				err = ErrOperationalPolicyUnavailable
			}
			if err == nil {
				err = s2StrictJSON(raw, &response, DefaultPolicyLimits())
			}
		}
		if err != nil {
			return nil, err
		}
		if response.Voter.Signer != member.ID || response.Voter.Role != operationalPolicySupportVoter || !verifyOperationalPolicySupportAttestation(k.trust, statement, response.Voter) {
			return nil, errOperationalPolicySupport
		}
		voters = append(voters, response.Voter)
		for _, proof := range response.Origins {
			origin, ok := k.trust.origin(proof.Signer)
			if !ok || origin.Member != member.ID || proof.Role != operationalPolicySupportOrigin || !verifyOperationalPolicySupportAttestation(k.trust, statement, proof) {
				return nil, errOperationalPolicySupport
			}
			origins = append(origins, proof)
		}
	}
	sort.Slice(origins, func(i, j int) bool { return origins[i].Signer < origins[j].Signer })
	raw, err := encodeOperationalPolicySupportCertificate(k.trust, profile, h, voters, origins)
	if err != nil {
		return nil, err
	}
	return encodeOperationalPolicyCandidate(k.trust, h, raw)
}

func (o *s3aOwner) ensureOperationalPolicyCandidate(ctx context.Context, digest [32]byte) error {
	k := o.kernel
	k.gate.Lock()
	if k.readyLocked() != nil {
		k.gate.Unlock()
		return ErrAuthorityUnavailable
	}
	h := k.origins[digest]
	if !operationalPolicySupportRequired(k.replayState, h) || h.candidate != "" {
		k.gate.Unlock()
		return nil
	}
	k.gate.Unlock()
	if o.origin == nil {
		return ErrOperationalPolicyUnavailable
	}
	candidate, err := o.origin.collectOperationalPolicySupport(ctx, h)
	if err != nil {
		return err
	}
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.chosen != nil {
		return ErrAuthorityUnavailable
	}
	if !operationalPolicySupportRequired(k.replayState, h) {
		return nil
	}
	old := k.origins[digest]
	if old == nil || old.raw != h.raw {
		return ErrChangeConflict
	}
	if old.candidate != "" {
		return nil
	}
	return k.persistOperationalPolicyCandidateLocked(candidate)
}
