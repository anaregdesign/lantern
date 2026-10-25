package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

// WithConfiguredTrust returns a detached startup selection. Desired content is
// required again on every generated-config start; it is never read from state
// or written into provisioning material. Historical genesis remains unchanged.
func (p *CurrentProvisioning) WithConfiguredTrust(desired *ConfiguredTrust) (*CurrentProvisioning, error) {
	if p == nil || p.config.Participant.Trust == nil || !desired.valid() {
		return nil, errS3AConfig
	}
	t := p.config.Participant.Trust
	if configuredVoterCapabilityFor(t, p.config.Participant.Member) == [32]byte{} {
		return nil, errS3AConfig
	}
	if p.config.Participant.OwnedOrigin != 0 {
		d, known := t.origin(p.config.Participant.OwnedOrigin)
		if !known || d.Member != p.config.Participant.Member || configuredTrustCapability(t, d) == [32]byte{} {
			return nil, errS3AConfig
		}
	}
	copy := *p
	copy.config = p.config
	copy.config.Participant = p.config.Participant
	detached, err := NewConfiguredTrust(desired.issuers)
	if err != nil {
		return nil, err
	}
	copy.config.Participant.configured = detached
	return &copy, nil
}

func (o *authorityOriginOwner) configuredReadyLocked() bool {
	return o.network.kernel.configuredReadyLocked()
}

func (k *s2cParticipant) configuredReadyLocked() bool {
	desired := k.config.configured
	if desired == nil {
		// Independently provisioned historical profiles have no config capability.
		// An enrolled current profile may never use omission as a serving fallback.
		for _, member := range k.trust.members {
			if member.ConfiguredTrust != 0 {
				return false
			}
		}
		for _, origin := range k.trust.origins {
			if origin.ConfiguredTrust != 0 {
				return false
			}
		}
		return true
	}
	p := k.replayState.projection
	return desired.valid() && p.snapshot.configuredTrustDigest == hex.EncodeToString(desired.digest[:])
}

// Called only after independent material validation and native M/P/B custody.
// The private listener is allowed to obtain endorsements and recover already
// certified values. All public admission/output separately checks desiredmatch.
func (o *authorityOriginOwner) configuredLoop() {
	defer o.network.workers.Done()
	n, k := o.network, o.network.kernel
	ctx := n.ctx
	var recovery configuredRecovery
	var hintSlot uint64
	var hintPrefix [32]byte
	ids := make([]uint32, 0, len(n.peers))
	for id := range n.peers {
		if id != k.config.Member {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	nextHint := 0
	retry := 100 * time.Millisecond
	self, _ := k.trust.member(k.config.Member)
	for ctx.Err() == nil {
		k.gate.Lock()
		if o.closed || k.readyLocked() != nil {
			k.gate.Unlock()
			return
		}
		matchesDesired := o.configuredReadyLocked()
		needsRecovery := self.Proposer && (recovery.neededLocked(k) || hintSlot != 0 && hintSlot == k.replayState.slot+1 && hintPrefix == k.replayState.prefix)
		canPlan := k.chosen == nil && self.Proposer && o.descriptor.Namespace != 0
		var statement configuredTrustStatement
		var planErr error
		observed, previous := k.replayState.projection.cut, k.replayState.prefix
		if canPlan && !matchesDesired {
			_, planErr = assessConfiguredTrust(k.replayState.projection, k.config.configured)
			statement = configuredTrustStatement{configuredTrustVersion, k.trust.scope, observed, previous, k.config.configured.Digest(), k.replayState.slot, sha256.Sum256([]byte(authorityTimeProfileDescription))}
		}
		k.gate.Unlock()
		attempt, cancel := context.WithTimeout(ctx, n.limits.ScheduleWindow)
		var candidate [32]byte
		if canPlan && !matchesDesired && planErr == nil {
			endorsements, err := o.collectConfiguredEndorsements(attempt, statement)
			if err == nil {
				candidate, _ = o.sealConfigured(attempt, statement, endorsements)
			}
		}
		idle := matchesDesired && !needsRecovery
		if idle && len(ids) > 0 {
			// Healthy/current owners rotate one read-only peer lookup at renewal-scale
			// cadence. A later hidden ACCEPT must still wake a surviving proposer.
			hint, _ := n.configuredRecoveryHint(attempt, ids[nextHint%len(ids)])
			nextHint++
			if hint != nil {
				hintSlot, hintPrefix = hint.slot, hint.predecessor
				needsRecovery = self.Proposer
			}
		} else {
			for _, id := range ids {
				_, _ = n.CatchUp(attempt, id)
			}
			if self.Proposer && !needsRecovery && candidate == [32]byte{} && len(ids) > 0 {
				hint, _ := n.configuredRecoveryHint(attempt, ids[nextHint%len(ids)])
				nextHint++
				if hint != nil {
					hintSlot, hintPrefix = hint.slot, hint.predecessor
					needsRecovery = true
				}
			}
		}
		// Matching desired content never substitutes for closing an accepted
		// suffix. Only a genuine phase-one quorum may select that retained value.
		if self.Proposer && (candidate != [32]byte{} || needsRecovery) {
			if _, err := n.Drive(attempt, candidate); err == nil {
				k.gate.Lock()
				recovery.completedLocked(k)
				k.gate.Unlock()
			}
		}
		cancel()
		k.gate.Lock()
		progressed := k.replayState.prefix != previous || k.replayState.projection.cut != observed
		k.gate.Unlock()
		delay := retry
		if progressed {
			retry = 100 * time.Millisecond
			delay = retry
		} else if idle && !needsRecovery {
			delay = 5 * time.Second
			retry = 100 * time.Millisecond
		} else {
			retry = min(5*time.Second, 2*retry)
		}
		// The worker remains available after startup: later retained accepted
		// evidence may outlive its original proposer. Empty/failed hints never
		// append NOOPs; repeated incompatible inputs use bounded backoff.
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (o *authorityOriginOwner) sealConfigured(ctx context.Context, s configuredTrustStatement, votes []configuredTrustEndorsement) ([32]byte, error) {
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if ctx.Err() != nil || o.closed || len(o.key) != ed25519.PrivateKeySize || configuredTrustCapability(k.trust, o.descriptor) == [32]byte{} || k.readyLocked() != nil || k.chosen != nil || !k.config.configured.valid() || s.Desired != k.config.configured.Digest() || s.Reviewed != k.replayState.projection.cut || s.Predecessor != k.replayState.prefix || s.Slot != k.replayState.slot || !verifyConfiguredEndorsements(k.trust, s, votes) || k.originSerial == ^uint64(0) {
		return [32]byte{}, ErrAuthorityUnavailable
	}
	// Reuse exact pending bytes if an earlier bounded drive did not yet choose.
	for digest, h := range k.pending {
		if h.handoff.configured != nil && h.handoff.configured.predecessor == s.Predecessor && h.handoff.configured.desired.Digest() == s.Desired {
			return digest, nil
		}
	}
	op, err := newConfiguredOperation(s.Reviewed, k.config.configured)
	if err != nil {
		return [32]byte{}, err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return [32]byte{}, err
	}
	id := FullChangeID{S1Version, s.Reviewed.Domain, s.Reviewed.Cohort, o.descriptor.Namespace, nonce}
	handoff := &S1Handoff{id: id, operation: op, origin: k.trust.originDigests[o.descriptor.ID], serial: k.originSerial + 1, configured: &authorityConfiguredAuthorization{desired: k.config.configured, predecessor: s.Predecessor, slot: s.Slot, capability: configuredTrustCapability(k.trust, o.descriptor)}}
	start, expiry, err := o.eligibilityLocked(ctx)
	if err != nil {
		return [32]byte{}, err
	}
	now, err := o.network.timeOwner.current()
	if err != nil {
		return [32]byte{}, err
	}
	low, high := time.Unix(0, int64(now.utc.low)).UTC(), time.Unix(0, int64(now.utc.high)).UTC()
	if low.Before(start) || !high.Before(expiry) || !verifyConfiguredEndorsementWindow(votes, low, high) {
		return [32]byte{}, ErrAuthorityUnavailable
	}
	header := configuredTrustHeader{Slot: s.Slot, ConsumeLow: low, ConsumeHigh: high, Version: configuredTrustVersion, Scope: k.trust.scope, OriginID: o.descriptor.ID, OriginDigest: handoff.origin, Incarnation: o.descriptor.Incarnation, ID: id, Serial: handoff.serial, Reviewed: s.Reviewed, Predecessor: s.Predecessor, Desired: s.Desired, Operation: op.digest, Value: handoff.digest(), Endorsements: votes}
	body, err := configuredTrustBody(header, op.Encode(), k.trust.bounds)
	if err != nil {
		return [32]byte{}, err
	}
	if uint64(len(k.pending)) >= k.config.PendingCount || k.pendingBytes > k.config.PendingBytes || uint64(len(body)+ed25519.SignatureSize) > k.config.PendingBytes-k.pendingBytes {
		return [32]byte{}, errS2CCapacity
	}
	// Preview authenticates and measures the full deterministic successor before
	// reserving native capacity. It confers no certificate or Apply permission.
	raw := append(body, ed25519.Sign(o.key, body)...)
	historical, err := verifyConfiguredTrustH(k.trust, raw)
	if err != nil {
		return [32]byte{}, err
	}
	preview, err := s2cPreviewCandidate(k.trust, k.b.state, k.trust.genesis.roots, k.config.BScope, k.b.receipt, historical, s2cLogicalCommit(k.trust, k.replayState.slot+1, historical.digest()))
	if err != nil {
		return [32]byte{}, err
	}
	credit, err := k.completionCredit(historical, preview)
	if err != nil {
		return [32]byte{}, err
	}
	charge, err := s2cPCharge(uint64(len(raw)))
	if err != nil {
		return [32]byte{}, err
	}
	credit.PBytes, err = s2CheckedAdd(credit.PBytes, charge)
	if err != nil || credit.PRecords == ^uint64(0) {
		return [32]byte{}, errS2CCapacity
	}
	credit.PRecords++
	if _, err = k.reserveOriginLocked(id, op.digest, credit); err != nil {
		return [32]byte{}, err
	}
	digest, err := k.persistSealedOriginLocked(raw)
	if err != nil && k.unknown != nil {
		return [32]byte{}, errors.Join(errS2CUnknown, err)
	}
	return digest, err
}

// Scheduling memory only; it is not an authorization or persistent floor.
// One successful phase one closes the startup suffix. Later installed choices
// do not require empty-slot recovery: local ACCEPT/SELECT or a verified remote
// accepted hint wakes recovery for the next suffix without a NOOP per mutation.
type configuredRecovery struct{ completed bool }

func (r *configuredRecovery) neededLocked(k *s2cParticipant) bool {
	return !r.completed || k.accepted != nil || k.selected != nil
}
func (r *configuredRecovery) completedLocked(_ *s2cParticipant) { r.completed = true }
