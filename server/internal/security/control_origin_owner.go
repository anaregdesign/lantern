package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net"
	"net/url"
	"sync/atomic"
	"time"
)

type authorityOriginOwner struct {
	network       *s3aOwner
	descriptor    s2cOriginDescriptor
	key           ed25519.PrivateKey // Owned by network.kernel.gate.
	purposes      *ManagementAuthorizations
	browserOrigin string
	inputs        chan struct{}
	assemblies    chan struct{}
	requests      map[*authorityOriginRequest]uint64
	requestBytes  uint64
	closed        bool
	outputs       authorityOutputPool
	outputHooks   atomic.Pointer[authorityOutputHooks]
	hooks         *authorityOriginHooks
}

// Timing/failure checkpoints only; never credential, signature or time seams.
type authorityOriginHooks struct{ beforeSample, afterConsume, afterSeal, afterDurable func() }

type authorityOriginRequest struct {
	owner      *authorityOriginOwner
	id         FullChangeID
	operation  OperationIdentity
	credential authorityCredential
}

// Private origin acknowledgement only: these bytes are available for the
// existing quorum driver after native ORIGIN_H durability, never current output.
type authorityOriginResult struct {
	digest  [32]byte
	raw     string
	outcome *OriginalOutcome
}

func loadAuthorityOriginKey(c s3aConfig, path string) (ed25519.PrivateKey, error) {
	if c.Participant.Trust == nil {
		return nil, errS3AConfig
	}
	d, known := c.Participant.Trust.origin(c.Participant.OwnedOrigin)
	if !known || d.Profile != authorityAdmissionProfile() || d.Member != c.Participant.Member {
		return nil, errS3AConfig
	}
	raw, err := s3aReadProvisioned(path, ed25519.PrivateKeySize)
	if err != nil {
		return nil, err
	}
	key := ed25519.PrivateKey(raw)
	valid := len(key) == ed25519.PrivateKeySize
	if valid {
		valid = bytes.Equal(ed25519.NewKeyFromSeed(key[:32]), key) && bytes.Equal(key.Public().(ed25519.PublicKey), d.PublicKey[:])
	}
	spki := sha256.Sum256(s3aVotingSPKI(d.PublicKey))
	for _, member := range c.Membership.Profile.Voters {
		if member.Key == d.PublicKey || member.Workload.SPKI == spki {
			valid = false
		}
	}
	if !valid {
		clear(key)
		return nil, errS3AConfig
	}
	return key, nil
}

// The caller transfers the independently loaded key only after M/P/B ownership
// succeeds. Ordinary public startup remains unwired; no decoded H can attach it.
func attachAuthorityOrigin(n *s3aOwner, key ed25519.PrivateKey, browserOrigin string) (*authorityOriginOwner, error) {
	u, err := url.Parse(browserOrigin)
	if n == nil || n.receiver == nil || n.timeOwner == nil || n.origin != nil || n.closed.Load() || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(browserOrigin) > 2048 {
		return nil, errS3AConfig
	}
	d, known := n.kernel.trust.origin(n.kernel.config.OwnedOrigin)
	if !known || d.Profile != authorityAdmissionProfile() || len(key) != ed25519.PrivateKeySize || !bytes.Equal(key.Public().(ed25519.PublicKey), d.PublicKey[:]) {
		return nil, errS3AConfig
	}
	o := &authorityOriginOwner{network: n, descriptor: d, key: key, purposes: NewManagementAuthorizations(10*time.Minute, 1024), browserOrigin: browserOrigin, inputs: make(chan struct{}, 4), assemblies: make(chan struct{}, 4), requests: make(map[*authorityOriginRequest]uint64)}
	o.outputs.connections = make(map[net.Conn]*authorityOutputConnection)
	n.origin = o
	return o, nil
}

func (o *authorityOriginOwner) closeOwned() {
	o.network.kernel.gate.Lock()
	defer o.network.kernel.gate.Unlock()
	o.closed = true
	o.purposes.Close()
	clear(o.key)
	o.key = nil
	clear(o.requests)
	o.requestBytes = 0
}

// Caller already registered with network.calls; Close cancels and joins every
// entered producer before key clearing or M/P/B lease release. Cryptographic
// verification, key fetching and native cookie parsing occur outside the gate.
func (o *authorityOriginOwner) verifyFacts(ctx context.Context, producer CurrentCredentialProducer) (CurrentCredentialView, CurrentCredentialFacts, error) {
	if producer == nil {
		return CurrentCredentialView{}, CurrentCredentialFacts{}, ErrPermissionDenied
	}
	select {
	case o.inputs <- struct{}{}:
	default:
		return CurrentCredentialView{}, CurrentCredentialFacts{}, errS3ACredit
	}
	defer func() { <-o.inputs }()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	stop := context.AfterFunc(o.network.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err := o.network.check(ctx); err != nil {
		return CurrentCredentialView{}, CurrentCredentialFacts{}, err
	}
	k := o.network.kernel
	k.gate.Lock()
	if o.closed || k.readyLocked() != nil || k.chosen != nil {
		k.gate.Unlock()
		return CurrentCredentialView{}, CurrentCredentialFacts{}, ErrAuthorityUnavailable
	}
	view := CurrentCredentialView{k.replayState.projection, o.network.timeOwner, o.browserOrigin}
	k.gate.Unlock()
	facts, err := producer.VerifyCurrentCredential(ctx, view)
	if err != nil {
		return CurrentCredentialView{}, CurrentCredentialFacts{}, err
	}
	if err = ctx.Err(); err != nil {
		return CurrentCredentialView{}, CurrentCredentialFacts{}, err
	}
	return view, facts, nil
}

func (o *authorityOriginOwner) authenticate(ctx context.Context, producer CurrentCredentialProducer) (authorityCredential, error) {
	view, facts, err := o.verifyFacts(ctx, producer)
	if err != nil {
		return authorityCredential{}, err
	}
	return captureAuthorityCredential(view, facts)
}

func (o *authorityOriginOwner) prepare(ctx context.Context, producer CurrentCredentialProducer, reviewed SemanticCut, command S1Command) (*authorityOriginRequest, error) {
	if o == nil || !o.network.enterCall() {
		return nil, errS3AClosed
	}
	defer o.network.calls.Done()
	select {
	case o.assemblies <- struct{}{}:
	default:
		return nil, errS3ACredit
	}
	defer func() { <-o.assemblies }()
	credential, err := o.authenticate(ctx, producer)
	if err != nil {
		return nil, err
	}
	if reviewed != credential.cut {
		return nil, ErrRevisionConflict
	}
	op, err := NewS1Operation(credential.actor, reviewed, command, o.network.kernel.trust.genesis.state.configuration.Policy)
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	r := &authorityOriginRequest{o, FullChangeID{S1Version, reviewed.Domain, reviewed.Cohort, o.descriptor.Namespace, nonce}, op, credential}
	charge := uint64(2*(len(op.canonical)+MaxAuthenticationEvidenceBytes) + 4096)
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if o.closed || k.readyLocked() != nil || !credential.matches(k.replayState.projection) || !r.id.valid() || r.id.Namespace <= k.replayState.retiredThrough {
		return nil, ErrAuthorityUnavailable
	}
	limit := min(k.config.PendingBytes, uint64(16<<20))
	if uint64(len(o.requests)) >= min(k.config.PendingCount, uint64(128)) || o.requestBytes > limit || charge > limit-o.requestBytes {
		return nil, errS3ACredit
	}
	o.requests[r], o.requestBytes = charge, o.requestBytes+charge
	return r, nil
}

func (o *authorityOriginOwner) discardLocked(r *authorityOriginRequest) {
	if charge, known := o.requests[r]; known {
		delete(o.requests, r)
		o.requestBytes -= charge
	}
}

func (o *authorityOriginOwner) discard(r *authorityOriginRequest) {
	if o == nil {
		return
	}
	o.network.kernel.gate.Lock()
	defer o.network.kernel.gate.Unlock()
	o.discardLocked(r)
}

func (o *authorityOriginOwner) knownLocked(r *authorityOriginRequest) (authorityOriginResult, bool, error) {
	k := o.network.kernel
	if r == nil || r.owner != o {
		return authorityOriginResult{}, false, ErrS1Contract
	}
	if known := k.replayState.ledger[r.id]; known != nil {
		if known.operation != r.operation {
			return authorityOriginResult{}, false, ErrChangeConflict
		}
		return authorityOriginResult{digest: known.handoff, outcome: known}, true, nil
	}
	if serial := k.originIDs[r.id]; serial != 0 {
		if reservation := k.originReservations[serial]; reservation.Operation != r.operation.digest {
			return authorityOriginResult{}, false, ErrChangeConflict
		}
		if digest, known := k.serials[serial]; known {
			h := k.origins[digest]
			return authorityOriginResult{digest: digest, raw: h.raw}, true, nil
		}
		// This reservation may have crossed consume. Only retained exact H can
		// recover; there is no absence certificate or replacement timestamp.
		return authorityOriginResult{}, false, errS2CUnknown
	}
	return authorityOriginResult{}, false, nil
}

func (o *authorityOriginOwner) eligibilityLocked(ctx context.Context) (time.Time, time.Time, error) {
	if o.closed || o.network.closed.Load() {
		return time.Time{}, time.Time{}, errS3AClosed
	}
	start, expiry, err := o.network.membership.CurrentValidity(ctx)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if o.network.identity.starts.After(start) {
		start = o.network.identity.starts
	}
	expiry = authorityMinTime(expiry, o.network.identity.expires)
	return start, expiry, nil
}

func authoritySampleClaim(now authorityCurrentTime, active *authorityRenewalActive) authorityConsumeTime {
	return authorityConsumeTime{now.profile, now.stamp.boot, now.stamp.process, now.stamp.nanos, active.started.stamp.nanos, now.sequence, now.utc.low, now.utc.high}
}

func (o *authorityOriginOwner) consume(ctx context.Context, r *authorityOriginRequest, producer CurrentCredentialProducer, proof [32]byte) (result authorityOriginResult, err error) {
	if o == nil || !o.network.enterCall() {
		return result, errS3AClosed
	}
	defer o.network.calls.Done()
	k := o.network.kernel
	// Resolve exact durable work before new credentials or purpose demands.
	k.gate.Lock()
	if err = k.readyLocked(); err == nil {
		var known bool
		result, known, err = o.knownLocked(r)
		if known || err != nil {
			k.gate.Unlock()
			return result, err
		}
	}
	k.gate.Unlock()
	if err != nil {
		return result, err
	}
	credential, err := o.authenticate(ctx, producer)
	if err != nil {
		return result, err
	}
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if err = k.readyLocked(); err != nil {
		return result, err
	}
	if knownResult, known, lookupErr := o.knownLocked(r); known || lookupErr != nil {
		return knownResult, lookupErr
	}
	if o.closed || o.requests[r] == 0 || k.chosen != nil || !credential.matches(k.replayState.projection) || credential.actor != r.operation.actor || credential.cut != r.operation.reviewed {
		return result, ErrAuthorityUnavailable
	}
	command, err := r.operation.command()
	if err != nil {
		return result, err
	}
	assessment, err := AssessS1(k.replayState.projection, r.operation)
	if err != nil {
		return result, err
	}
	if command.Kind == S1Management {
		access, active := k.replayState.projection.snapshot.AccessFor(credential.actor)
		if !active || !access.AllowsGlobal(SecurityManage) {
			return result, ErrPermissionDenied
		}
	}
	if k.originSerial == ^uint64(0) {
		return result, errS2CCapacity
	}
	h := authorityHistoricalHeader{Version: authorityAdmissionVersion, Scope: k.trust.scope, OriginID: o.descriptor.ID, OriginDigest: k.trust.originDigests[o.descriptor.ID], Incarnation: o.descriptor.Incarnation,
		ID: r.id, Serial: k.originSerial + 1, OperationDigest: r.operation.digest, Authentication: credential.authentication, Lineage: credential.lineage, Credential: credential.claim, Workloads: o.network.binding}
	h.CredentialDeadline = credential.claim.AdmissionDeadline
	o.purposes.mu.Lock()
	defer o.purposes.mu.Unlock()
	if assessment.NeedsPurpose {
		binding := authorityPurposeBinding{r.id, r.operation.digest, r.operation.reviewed, credential.actor, credential.authentication.IssuerConfigRevision, credential.lineage}
		p, err := o.purposes.prepareCurrentConsumeLocked(proof, binding, h.OriginDigest, h.Serial)
		if err != nil {
			return result, err
		}
		h.Purpose, h.PurposeDeadline = &p, p.ExpiresAt
		h.PurposeEvidence, h.PurposeBinding = s2cHash("current-purpose-evidence-v2", p), s1PurposeBinding(r.id, r.operation)
	} else if proof != [32]byte{} {
		return result, ErrOperationAuthorization
	}
	start, expiry, err := o.eligibilityLocked(ctx)
	if err != nil {
		return result, err
	}
	// Preliminary readiness avoids burning a serial for an already-known time
	// refusal. Only the second sample, after the durable reservation, consumes.
	now, active, err := o.network.receiver.currentLocked()
	if err != nil {
		return result, err
	}
	h.Time = authoritySampleClaim(now, active)
	_, h.ConsumeUpper = h.Time.utcTimes()
	h.Renewal = []byte(active.certificate.raw)
	validTime := func() bool {
		low, high := h.Time.utcTimes()
		return !low.Before(start) && high.Before(expiry) && verifyAuthorityCredential(h, r.operation, command) && verifyAuthorityPurpose(h, r.operation)
	}
	if !validTime() {
		return result, ErrPermissionDenied
	}
	protected, hBound, err := authorityOriginCapacity(k, h, r.operation, assessment)
	if err != nil {
		return result, err
	}
	if _, err = k.reserveOriginLocked(r.id, r.operation.digest, protected); err != nil {
		return result, err
	}
	defer o.discardLocked(r)
	if o.hooks != nil && o.hooks.beforeSample != nil {
		o.hooks.beforeSample()
	}
	// All non-time inputs, purpose ownership and capacity are now frozen under
	// the common gate. This native sample is the sole successful consume event.
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if o.network.closed.Load() {
		return result, errS3AClosed
	}
	now, sameActive, err := o.network.receiver.currentLocked()
	if err != nil || sameActive != active {
		return result, errors.Join(ErrAuthorityUnavailable, err)
	}
	h.Time = authoritySampleClaim(now, active)
	_, h.ConsumeUpper = h.Time.utcTimes()
	if !validTime() {
		return result, ErrPermissionDenied
	}
	if h.Purpose != nil {
		o.purposes.markCurrentConsumedLocked(*h.Purpose)
	}
	if o.hooks != nil && o.hooks.afterConsume != nil {
		o.hooks.afterConsume()
	}
	h.CredentialEvidence = authorityCredentialEvidence(h)
	a := &s1VerifiedAuthorization{h.Authentication, h.Lineage, h.CredentialEvidence, h.PurposeEvidence, h.PurposeBinding, h.ConsumeUpper, h.CredentialDeadline, h.PurposeDeadline}
	h.Value = (&S1Handoff{h.ID, r.operation, h.OriginDigest, h.Serial, a}).digest()
	body, err := authorityHistoricalBody(h, r.operation.Encode(), k.trust.bounds)
	if err != nil || uint64(len(body)+64) > hBound {
		k.unknown = errors.Join(errS2CUnknown, err)
		return result, k.unknown
	}
	raw := append(body, ed25519.Sign(o.key, body)...)
	if o.hooks != nil && o.hooks.afterSeal != nil {
		o.hooks.afterSeal()
	}
	digest, err := k.persistSealedOriginLocked(raw)
	if err != nil {
		return result, err
	}
	if o.hooks != nil && o.hooks.afterDurable != nil {
		o.hooks.afterDurable()
	}
	return authorityOriginResult{digest: digest, raw: string(raw)}, nil
}
