package security

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

const logPolicySupportPath = "/lantern-private/control/log-policy-v1/support"

type logPolicySupportResponse struct {
	Voter   logPolicySupportAttestation
	Origins []logPolicySupportAttestation
}

func logPolicyCandidateBound(t *s2cTrust, profile CurrentProfile, originalBytes uint64) (uint64, error) {
	proof, err := logPolicySupportCertificateBound(t, profile)
	if err != nil {
		return 0, err
	}
	total, err := s2CheckedAdd(originalBytes, proof+uint64(len(logPolicyCandidateMagic)+8))
	if err != nil || total > t.bounds.HistoricalBytes || s2cChosenSizeBound(t, total) == 0 {
		return 0, errS2CCapacity
	}
	return total, nil
}

// No support signature is minted until an authentic reviewed Apply H exists,
// the resident owner is installed, the whole bounded successor is representable,
// and the local minimum-version floor has crossed real WAL+tip durability.
func (o *authorityOriginOwner) supportLogPolicyLocked(ctx context.Context, raw []byte) (logPolicySupportResponse, error) {
	k := o.network.kernel
	if ctx.Err() != nil || o.closed || k.readyLocked() != nil || k.chosen != nil || !o.configuredReadyLocked() || k.logRuntime == nil || k.logRuntime.PrepareLogPolicy() != nil {
		return logPolicySupportResponse{}, ErrLogPolicyUnavailable
	}
	h, err := verifyAuthorityHistoricalH(k.trust, raw)
	if err != nil || !hasLogPolicyChange(h.handoff) || h.handoff.operation.reviewed != k.replayState.projection.cut {
		return logPolicySupportResponse{}, errLogPolicySupport
	}
	profile, err := logPolicyProfileForOriginal(k.trust, h)
	if err != nil || profile != (&CurrentAuthority{origin: o}).Profile() {
		return logPolicySupportResponse{}, errLogPolicySupport
	}
	assessment, err := k.assessLogPolicyFloor(h)
	if err != nil {
		return logPolicySupportResponse{}, err
	}
	charge, err := authoritySuccessorCharge(k, h.handoff.id, h.handoff.operation, assessment)
	if err != nil {
		return logPolicySupportResponse{}, err
	}
	bound, err := logPolicyCandidateBound(k.trust, profile, uint64(len(h.raw)))
	if err != nil || bound > k.config.PendingBytes || s2cChosenSizeBound(k.trust, bound) > k.config.OutboxBytes/uint64(len(k.trust.members)) {
		return logPolicySupportResponse{}, errS2CCapacity
	}
	candidateCharge, err := s2cPCharge(bound)
	if err != nil {
		return logPolicySupportResponse{}, err
	}
	chosenCharge, err := s2cPCharge(s2cChosenSizeBound(k.trust, bound))
	if err != nil {
		return logPolicySupportResponse{}, err
	}
	drained, _ := s2cPCharge(0)
	protected := s2cMaxCredit(k.credit, s2cCredit{PBytes: candidateCharge + chosenCharge + drained, PRecords: 3, BBytes: charge, BRecords: 1})
	// Even an already-fenced participant checks the complete next candidate's
	// budget before endorsing another attempt. An unchosen H gains no promise
	// of future capacity beyond the existing accepted-completion credit.
	if !k.bCreditFits(protected) {
		return logPolicySupportResponse{}, errS2CCapacity
	}
	if k.logPolicyFloor.Index == 0 {
		floorRaw, err := logPolicyFloorBytes(h)
		if err != nil {
			return logPolicySupportResponse{}, err
		}
		encodedFloor, err := k.encodeRecord(s2cPRecord{kind: s2cPLogPolicyFloor, index: k.p.count + 1, raw: string(floorRaw)})
		if err != nil {
			return logPolicySupportResponse{}, err
		}
		if err = k.p.checkAppend(encodedFloor, protected.PBytes, protected.PRecords); err != nil {
			return logPolicySupportResponse{}, err
		}
	} else if k.p.used > k.p.policy.Bytes || protected.PBytes > k.p.policy.Bytes-k.p.used || k.p.count > k.p.policy.Records || protected.PRecords > k.p.policy.Records-k.p.count {
		return logPolicySupportResponse{}, errS2CCapacity
	}
	start, expiry, timeErr := o.eligibilityLocked(ctx)
	if timeErr != nil {
		return logPolicySupportResponse{}, timeErr
	}
	now, _, sampleErr := o.network.receiver.currentLocked()
	err = sampleErr
	if sampleErr == nil {
		low, high := time.Unix(0, int64(now.utc.low)), time.Unix(0, int64(now.utc.high))
		if low.Before(start) || !high.Before(expiry) {
			return logPolicySupportResponse{}, ErrAuthorityUnavailable
		}
	}
	if err != nil {
		return logPolicySupportResponse{}, err
	}
	if err = k.ensureLogPolicyFloorLocked(h, protected); err != nil {
		return logPolicySupportResponse{}, err
	}
	statement, err := newLogPolicySupportStatement(k.trust, profile, h)
	if err != nil {
		return logPolicySupportResponse{}, err
	}
	response := logPolicySupportResponse{Voter: logPolicySupportAttestation{Role: logPolicySupportVoter, Signer: k.config.Member, Floor: k.logPolicyFloor}, Origins: []logPolicySupportAttestation{}}
	body, err := logPolicySupportAttestationBody(statement, response.Voter)
	if err != nil {
		return logPolicySupportResponse{}, err
	}
	response.Voter.Signature = ed25519.Sign(k.key, body)
	if o.descriptor.ID != 0 {
		origin := logPolicySupportAttestation{Role: logPolicySupportOrigin, Signer: o.descriptor.ID, Floor: k.logPolicyFloor}
		body, err = logPolicySupportAttestationBody(statement, origin)
		if err != nil {
			return logPolicySupportResponse{}, err
		}
		if len(o.key) != ed25519.PrivateKeySize {
			return logPolicySupportResponse{}, ErrLogPolicyUnavailable
		}
		origin.Signature = ed25519.Sign(o.key, body)
		response.Origins = append(response.Origins, origin)
	}
	return response, nil
}

func (n *s3aOwner) logPolicySupportResponse(ctx context.Context, raw []byte, check func() error) ([]byte, error) {
	if n.origin == nil || check == nil {
		return nil, ErrLogPolicyUnavailable
	}
	k := n.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if check() != nil {
		return nil, ErrAuthorityUnavailable
	}
	response, err := n.origin.supportLogPolicyLocked(ctx, raw)
	if err != nil {
		return nil, err
	}
	if check() != nil {
		return nil, ErrAuthorityUnavailable
	}
	return json.Marshal(response)
}

func (o *authorityOriginOwner) collectLogPolicySupport(ctx context.Context, h *s2cHistoricalH) (*s2cHistoricalH, error) {
	n, k := o.network, o.network.kernel
	profile, err := logPolicyProfileForOriginal(k.trust, h)
	if err != nil {
		return nil, err
	}
	statement, err := newLogPolicySupportStatement(k.trust, profile, h)
	if err != nil {
		return nil, err
	}
	voters := make([]logPolicySupportAttestation, 0, len(k.trust.members))
	origins := make([]logPolicySupportAttestation, 0, len(k.trust.origins))
	for _, member := range k.trust.members {
		var response logPolicySupportResponse
		if member.ID == k.config.Member {
			k.gate.Lock()
			response, err = o.supportLogPolicyLocked(ctx, []byte(h.raw))
			k.gate.Unlock()
		} else {
			var raw []byte
			var status int
			raw, status, _, err = n.requestAdmitted(ctx, member.ID, logPolicySupportPath, []byte(h.raw), logPolicySupportCertificateMax)
			if err == nil && status != http.StatusOK {
				err = ErrLogPolicyUnavailable
			}
			if err == nil {
				err = s2StrictJSON(raw, &response, DefaultPolicyLimits())
			}
		}
		if err != nil {
			return nil, err
		}
		if response.Voter.Signer != member.ID || response.Voter.Role != logPolicySupportVoter || !verifyLogPolicySupportAttestation(k.trust, statement, response.Voter) {
			return nil, errLogPolicySupport
		}
		voters = append(voters, response.Voter)
		for _, proof := range response.Origins {
			origin, ok := k.trust.origin(proof.Signer)
			if !ok || origin.Member != member.ID || proof.Role != logPolicySupportOrigin || !verifyLogPolicySupportAttestation(k.trust, statement, proof) {
				return nil, errLogPolicySupport
			}
			origins = append(origins, proof)
		}
	}
	sort.Slice(origins, func(i, j int) bool { return origins[i].Signer < origins[j].Signer })
	raw, err := encodeLogPolicySupportCertificate(k.trust, profile, h, voters, origins)
	if err != nil {
		return nil, err
	}
	return encodeLogPolicyCandidate(k.trust, h, raw)
}

func (n *s3aOwner) ensureLogPolicyCandidate(ctx context.Context, digest [32]byte) error {
	k := n.kernel
	k.gate.Lock()
	if k.readyLocked() != nil {
		k.gate.Unlock()
		return ErrAuthorityUnavailable
	}
	h := k.origins[digest]
	if h == nil || !hasLogPolicyChange(h.handoff) || k.replayState.projection.snapshot.LogPolicy() != nil || h.handoff.operation.reviewed != k.replayState.projection.cut || h.candidate != "" {
		k.gate.Unlock()
		return nil
	}
	k.gate.Unlock()
	if n.origin == nil {
		return ErrLogPolicyUnavailable
	}
	candidate, err := n.origin.collectLogPolicySupport(ctx, h)
	if err != nil {
		return err
	}
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.chosen != nil {
		return ErrAuthorityUnavailable
	}
	if k.replayState.projection.snapshot.LogPolicy() != nil {
		return nil
	}
	old := k.origins[digest]
	if old == nil || old.raw != h.raw {
		return ErrChangeConflict
	}
	if old.candidate != "" {
		return nil
	}
	return k.persistLogPolicyCandidateLocked(candidate)
}
