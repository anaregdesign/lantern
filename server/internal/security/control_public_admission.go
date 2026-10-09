package security

import (
	"context"
	"encoding/hex"
	"time"
)

// CurrentPublicVersion names the new public contract. Legacy scalar revisions,
// 16-byte change IDs and ENFORCED statuses retain their original meanings.
const CurrentPublicVersion uint32 = 2

// CurrentProfile is public, non-secret configuration identity. Possession is
// not serving authority. Complete cut equality is never scalar ordering.
type CurrentProfile struct {
	Version                    uint32
	Domain, Cohort             [32]byte
	Generation                 [16]byte
	Protocol, Time, Membership [32]byte
	Configuration              [32]byte
}

// Binding identifies the selected profile in the independently signed data
// workload domain. It is never a writer key or serving permission.
func (p CurrentProfile) Binding() string {
	if p.Version != CurrentPublicVersion || p.Domain == [32]byte{} || p.Cohort == [32]byte{} || p.Generation == [16]byte{} || p.Protocol == [32]byte{} || p.Time == [32]byte{} || p.Membership == [32]byte{} || p.Configuration == [32]byte{} {
		return ""
	}
	d := s2cHash("public-profile-v2", p)
	return "current-v2:" + hex.EncodeToString(d[:])
}

// CurrentAuthority is the restricted public-composition facade. It exposes no
// signer, arbitrary clock, decoded H constructor, journal handle or ready bool.
// Only the production constructor attaches an owned current origin to it.
type CurrentAuthority struct {
	origin       *authorityOriginOwner
	observations currentPublicObservations
}

type currentPublicAdmission struct {
	owner      *CurrentAuthority
	credential authorityCredential
	snapshot   *Snapshot
	profile    CurrentProfile
	binding    [32]byte
}

// OriginEnabled is a routing capability, not readiness or authorization.
func (o *CurrentAuthority) OriginEnabled() bool {
	return o != nil && o.origin != nil && o.origin.descriptor.Namespace != 0
}

func (o *CurrentAuthority) MemberID() uint32 {
	if o == nil || o.origin == nil {
		return 0
	}
	return o.origin.network.kernel.config.Member
}

func (o *CurrentAuthority) Profile() CurrentProfile {
	if o == nil || o.origin == nil {
		return CurrentProfile{}
	}
	n := o.origin.network
	g := n.kernel.trust.genesis.state
	c := g.projection.cut
	return CurrentProfile{CurrentPublicVersion, c.Domain, c.Cohort, c.Generation,
		n.kernel.trust.scope, n.timeOwner.profile, n.binding, g.configuration.Digest()}
}

// Admit verifies actual credentials against the installed S1 view, then samples
// live renewal/time under the participant gate. It never synthesizes a signed
// writer Revision, a short wall deadline, or authority from Snapshot alone.
func (o *CurrentAuthority) Admit(ctx context.Context, producer CurrentCredentialProducer) (*Admission, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return nil, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	view, facts, err := o.origin.verifyFacts(ctx, producer)
	if err != nil {
		return nil, err
	}
	c, err := captureAuthorityUseCredential(view, facts, false)
	if err != nil {
		return nil, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.chosen != nil {
		return nil, ErrAuthorityUnavailable
	}
	if _, err = o.origin.authorizeCurrentCredentialLocked(ctx, c); err != nil {
		return nil, err
	}
	access, active := view.Snapshot().AccessFor(c.actor)
	if !active {
		return nil, ErrPermissionDenied
	}
	current := &currentPublicAdmission{owner: o, credential: c, snapshot: view.Snapshot(), profile: o.Profile()}
	current.binding = s2cHash("public-admission-v2", struct {
		Profile         CurrentProfile
		Cut             SemanticCut
		Actor           Identity
		Lineage         uint64
		Authentication  Authentication
		Evidence        [32]byte
		BrowserReadOnly bool
	}{current.profile, c.cut, c.actor, c.lineage, c.authentication, c.claim.Verification, c.browserReadOnly})
	authTime := time.Time{}
	if c.claim.Token != nil && c.claim.Token.AuthTime.Numeric {
		authTime = c.claim.Token.AuthTime.Time()
	} else if c.claim.Session != nil {
		authTime = c.claim.Session.AuthTime
	}
	return &Admission{identity: c.actor, authTime: authTime, expiresAt: c.claim.AdmissionDeadline,
		browser: c.claim.Session != nil, access: access, authentication: c.authentication, current: current}, nil
}

func (a *currentPublicAdmission) check(ctx context.Context) error {
	if a == nil || a.owner == nil || a.owner.origin == nil || !a.owner.origin.network.enterCall() {
		return ErrAuthorityUnavailable
	}
	o := a.owner.origin
	defer o.network.calls.Done()
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.chosen != nil {
		return ErrAuthorityUnavailable
	}
	_, err := o.authorizeCurrentCredentialLocked(ctx, a.credential)
	return err
}

// CurrentCut and CurrentProfile carry immutable identity only. Check is still
// required at each admission/output event; these values cannot refresh a view.
func (a *Admission) CurrentCut() (SemanticCut, bool) {
	if a == nil || a.current == nil {
		return SemanticCut{}, false
	}
	return a.current.credential.cut, true
}

func (a *Admission) CurrentProfile() (CurrentProfile, bool) {
	if a == nil || a.current == nil {
		return CurrentProfile{}, false
	}
	return a.current.profile, true
}

func (a *Admission) BrowserReadOnly() bool {
	return a != nil && a.current != nil && a.current.credential.browserReadOnly
}

type currentRequestKey struct{}
type currentRequestCredential struct {
	owner    *CurrentAuthority
	producer CurrentCredentialProducer
}

// WithRequestCredential is a trusted authentication adapter boundary. The
// producer is process-local request state; no decoded facts/signature/public
// header can substitute for it. Refresh explicitly re-evaluates that producer
// against the newly installed cut after Apply, without a second Code exchange.
func (o *CurrentAuthority) WithRequestCredential(ctx context.Context, producer CurrentCredentialProducer) (context.Context, *Admission, error) {
	a, err := o.Admit(ctx, producer)
	if err != nil {
		return ctx, nil, err
	}
	ctx = context.WithValue(ctx, currentRequestKey{}, currentRequestCredential{o, producer})
	return WithAdmission(ctx, a), a, nil
}

func (o *CurrentAuthority) RequestCredential(ctx context.Context) (CurrentCredentialProducer, error) {
	r, ok := ctx.Value(currentRequestKey{}).(currentRequestCredential)
	if !ok || r.owner != o || r.producer == nil {
		return nil, ErrPermissionDenied
	}
	return r.producer, nil
}

func (o *CurrentAuthority) RefreshRequest(ctx context.Context) (context.Context, *Admission, error) {
	p, err := o.RequestCredential(ctx)
	if err != nil {
		return ctx, nil, err
	}
	// Only the explicit control/session post-Apply path may request a new
	// renewal here. Ordinary Admit/Check/data/output never makes peer calls.
	if !o.Ready(ctx) {
		// An Apply can reach the local projection before enough peers have
		// installed that cut to answer its fresh challenge. This is a finite
		// disclosure wait, never another consume/Apply or an authority timer.
		wait, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		for !o.Ready(wait) {
			if wait.Err() != nil || o.origin.network.ctx.Err() != nil {
				return ctx, nil, ErrAuthorityUnavailable
			}
			if _, _, err := o.TimeBounds(); err != nil {
				return ctx, nil, ErrAuthorityUnavailable
			}
			_ = o.origin.network.renewAuthority(wait)
			if o.Ready(wait) {
				break
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-wait.Done():
				timer.Stop()
				return ctx, nil, ErrAuthorityUnavailable
			case <-timer.C:
			}
		}
	}
	return o.WithRequestCredential(ctx, p)
}
