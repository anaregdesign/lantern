package security

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// Historical genesis and participant contracts are embedded in independently
// supplied private authority JSON. Neither peer responses nor mutable runtime
// state supply them. Current startup intent is provided separately each time.
type currentGenesisDocument struct {
	Version        uint32
	Domain, Cohort [32]byte
	Generation     [16]byte
	Fences         [32]byte
	Image          Image
	Execution      S1ExecutionConfig
	Roots          s2CapsuleRoots
	Members        []s2cMember
	Origins        []s2cOriginDescriptor
	Bounds         s2cBounds
	TimeProfile    [32]byte
}

type currentParticipantDocument struct {
	Member                                  uint32
	Incarnation                             [16]byte
	PPath, BPath                            string
	PIdentity                               [32]byte
	PEpoch                                  [16]byte
	PPolicy                                 s2cJournalPolicy
	BScope                                  s2LocalScope
	OwnedOrigin                             uint32
	PendingBytes, PendingCount, OutboxBytes uint64
}

// CurrentProvisioning owns detached original validation inputs. It contains no
// opened journal or signer, injected time source or assertion of readiness.
type CurrentProvisioning struct {
	config      s3aConfig
	floors      string
	listen      string
	localListen string // Current invocation only; never rewrites historical custody binding.
	profile     CurrentProfile
	jsonSource  []byte
	binding     [32]byte
	paths       []string
	manifest    string
}

func (p *CurrentProvisioning) Profile() CurrentProfile {
	if p == nil {
		return CurrentProfile{}
	}
	return p.profile
}

// FixedVoterCount reports the validated original fixed topology without
// opening mutable custody or asserting that any participant is ready.
func (p *CurrentProvisioning) FixedVoterCount() int {
	if p == nil || p.config.Participant.Trust == nil {
		return 0
	}
	return len(p.config.Participant.Trust.members)
}

// WithLocalListen selects this invocation's local control bind address. Its
// port remains the independently pinned advertised control port; changing a
// local host never rewrites original material, peer destinations or custody.
func (p *CurrentProvisioning) WithLocalListen(address string) (*CurrentProvisioning, error) {
	if p == nil || currentJSONEnrollment(p.config, address, p.config.Participant.OwnedOrigin != 0) != nil {
		return nil, errS3AConfig
	}
	copy := *p
	copy.listen, copy.localListen = address, address
	return &copy, nil
}

// OpenCurrentAuthority uses only the production native constructor. Its private
// listener starts before public readiness; quorum absence never falls back to
// OFF or a fixed writer. The owner joins listener/workers/producers on Close.
func OpenCurrentAuthority(ctx context.Context, p *CurrentProvisioning, mode, browserOrigin string) (_ *CurrentAuthority, err error) {
	if p == nil || p.config.Participant.Trust == nil || p.profile.Version != CurrentPublicVersion || mode != "fresh" && mode != "resume" {
		return nil, errS3AConfig
	}
	for _, member := range p.config.Participant.Trust.members {
		if member.ConfiguredTrust != 0 && !p.config.Participant.configured.valid() {
			return nil, errS3AConfig
		}
	}
	for _, origin := range p.config.Participant.Trust.origins {
		if origin.ConfiguredTrust != 0 && !p.config.Participant.configured.valid() {
			return nil, errS3AConfig
		}
	}
	p, custody, floors, err := openCurrentCustody(p, mode)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, custody.close())
		}
	}()
	o, err := openCurrentAuthorityOwnerWithCustody(ctx, p.config, "", browserOrigin, mode == "fresh", floors, custody)
	if err != nil {
		return nil, err
	}
	// Failures after private-owner construction also retain final counters.
	// Every return below closes that owner before this diagnostic is copied.
	defer func() {
		if !transferred && err != nil {
			err = withAuthorityTimeAcquisitionCounts(err, o.network.timeOwner.acquisitionCounts())
		}
	}()
	listener, err := net.Listen("tcp", p.listen)
	if err != nil {
		return nil, errors.Join(err, o.network.Close())
	}
	if err = o.network.Start(listener); err != nil {
		return nil, errors.Join(err, listener.Close(), o.network.Close())
	}
	if o.network.kernel.config.configured != nil {
		o.network.workers.Add(1)
		go o.configuredLoop()
	}
	transferred = true
	return &CurrentAuthority{origin: o, custody: custody, manifest: p.manifest}, nil
}

// ReloadMembership rereads only the fixed private manifest input. Readiness and
// the previous interval are deliberately not preconditions: an expired owner
// must still be able to receive a higher revision. Apply owns all acceptance,
// persistence and permanent-fence decisions under the existing kernel gate.
func (o *CurrentAuthority) ReloadMembership(ctx context.Context) error {
	if o == nil || o.origin == nil || o.origin.network == nil || !o.origin.network.enterCall() {
		return ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done() // Join the file read as well as Refresh.
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := currentCustodyPath(o.manifest)
	if err != nil || path != o.manifest {
		return errS3AConfig
	}
	raw, err := s3aReadProvisioned(path, peerauth.MaxManifestBytes)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Refresh retains its own enterCall and gate. Close may refuse it after the
	// read; an already-started checkpoint is never detached on cancellation.
	return o.origin.network.Refresh(raw)
}

// Close aborts ownership without declaring a normal restart checkpoint. Wire
// uses it for failed initialization; Shutdown is the explicit orderly path.
func (o *CurrentAuthority) Close() error {
	return o.finish(false)
}

func (o *CurrentAuthority) TimeBounds() (time.Time, time.Time, error) {
	if o == nil || o.origin == nil || o.origin.network.closed.Load() {
		return time.Time{}, time.Time{}, ErrAuthorityUnavailable
	}
	now, err := o.origin.network.timeOwner.current()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return time.Unix(0, int64(now.utc.low)).UTC(), time.Unix(0, int64(now.utc.high)).UTC(), nil
}

// TimeAcquisitionCounts is bounded local diagnostics, never an authority input
// or public RPC. Lock ordering matches owner -> producer at startup snapshots.
func (o *CurrentAuthority) TimeAcquisitionCounts() decisiontrace.AcquisitionCounts {
	if o == nil || o.origin == nil {
		return decisiontrace.AcquisitionCounts{}
	}
	return o.origin.network.timeOwner.acquisitionCounts()
}

// VerificationTime supports the existing verifier/cache clock port. Current
// admission independently verifies both endpoints; failure never uses wall time.
func (o *CurrentAuthority) VerificationTime() time.Time {
	_, high, err := o.TimeBounds()
	if err != nil {
		return time.Unix(0, int64(^uint64(0)>>1)).UTC()
	}
	return high
}

func (o *CurrentAuthority) Ready(ctx context.Context) bool {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		traceAuthorityBrowserDecision(nil, "current_ready", "owner")
		return false
	}
	n := o.origin.network
	defer n.calls.Done()
	n.kernel.gate.Lock()
	defer n.kernel.gate.Unlock()
	defer n.kernel.poisonPanic()
	switch {
	case ctx.Err() != nil:
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "context")
		return false
	case !o.origin.configuredReadyLocked():
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "configuration")
		return false
	case n.kernel.readyLocked() != nil:
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "kernel")
		return false
	case n.kernel.chosen != nil:
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "chosen")
		return false
	}
	start, expiry, err := o.origin.eligibilityLocked(ctx)
	if err != nil {
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "eligibility")
		return false
	}
	now, _, err := n.receiver.currentLocked()
	if err != nil {
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "renewal")
		return false
	}
	if time.Unix(0, int64(now.utc.low)).Before(start) {
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "before_start")
		return false
	}
	if !time.Unix(0, int64(now.utc.high)).Before(expiry) {
		traceAuthorityBrowserDecision(n.timeOwner, "current_ready", "expired")
		return false
	}
	return true
}

// ExportFloors returns a live minimum-cut observation, never a resume capability
// or a final shutdown checkpoint. Shutdown owns durable floor custody.
func (o *CurrentAuthority) ExportFloors() ([]byte, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return nil, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	p, b, err := o.origin.network.kernel.Floors()
	if err != nil {
		return nil, err
	}
	m, err := o.origin.network.membership.Floor()
	if err != nil {
		return nil, err
	}
	return json.Marshal(s3aFloors{M: m, P: p, B: b})
}
