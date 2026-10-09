package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// Both documents are independently provisioned private files. Neither a WAL,
// peer response, current projection nor an old fixed-writer Store supplies them.
// JSON is canonical (one trailing newline is allowed), preventing fixed-array
// truncation/padding, duplicate aliases or silent unknown configuration fields.
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

type currentMembershipDocument struct {
	Path, KeyFile, ManifestFile string
	Profile                     peerauth.ControlProfile
	Self                        peerauth.Member
}

type currentNodeDocument struct {
	Version       uint32
	GenesisFile   string
	GenesisSHA256 [32]byte
	Participant   currentParticipantDocument
	Membership    currentMembershipDocument
	Identity      s3aIdentityFiles
	OriginKeyFile string
	FloorsFile    string
	ListenAddress string
	Limits        s3aLimits
}

// CurrentProvisioning is an opaque, detached validation result. It contains no
// opened journal, signing key, injected time source or assertion of readiness.
type CurrentProvisioning struct {
	config    s3aConfig
	originKey string
	floors    string
	listen    string
	profile   CurrentProfile
}

func currentDocument(path string, limit int64, target any) ([]byte, error) {
	raw, err := s3aReadProvisioned(path, limit)
	if err != nil {
		return nil, errS3AConfig
	}
	if err = s2StrictJSON(bytes.TrimSpace(raw), target, DefaultPolicyLimits()); err != nil {
		return nil, errS3AConfig
	}
	return raw, nil
}

// LoadCurrentProvisioning validates independent files before any state is
// created. Paths/identities/budgets remain fixed across ordinary intact reopen.
func LoadCurrentProvisioning(path string) (*CurrentProvisioning, error) {
	var node currentNodeDocument
	if _, err := currentDocument(path, 256<<10, &node); err != nil || node.Version != CurrentPublicVersion {
		return nil, errS3AConfig
	}
	var genesis currentGenesisDocument
	raw, err := currentDocument(node.GenesisFile, MaxImageBytes+256<<10, &genesis)
	if err != nil || node.GenesisSHA256 == [32]byte{} || sha256.Sum256(raw) != node.GenesisSHA256 || genesis.Version != CurrentPublicVersion ||
		!genesis.Execution.valid() || genesis.Roots.Genesis == [32]byte{} || genesis.Roots.Retirement != [32]byte{} ||
		genesis.TimeProfile != sha256.Sum256([]byte(authorityTimeProfileDescription)) {
		return nil, errS3AConfig
	}
	// S4 only admits independent initial genesis and intact journal replay. A
	// later checkpoint/retirement or old writer image is not an import path.
	for _, origin := range genesis.Origins {
		if origin.Profile != authorityAdmissionProfile() {
			return nil, errS3AConfig
		}
	}
	projection, err := NewS1Projection(genesis.Image, genesis.Execution.Policy, genesis.Domain, genesis.Cohort, genesis.Fences, genesis.Generation)
	if err != nil {
		return nil, errS3AConfig
	}
	membership, err := s2cMemberSetDigest(genesis.Members)
	if err != nil {
		return nil, errS3AConfig
	}
	state, err := NewS1ApplyState(projection, membership, genesis.Execution.Capacity, S1Retention{})
	if err != nil {
		return nil, errS3AConfig
	}
	trust, err := newS2CTrust(s2cBootstrap{&s2TrustedGenesis{state, genesis.Roots}, genesis.Members, genesis.Origins, genesis.Bounds})
	if err != nil {
		return nil, errS3AConfig
	}
	p := node.Participant
	c := s3aConfig{Participant: s2cParticipantConfig{
		Trust: trust, Member: p.Member, Incarnation: p.Incarnation, PPath: p.PPath, BPath: p.BPath,
		PIdentity: p.PIdentity, PEpoch: p.PEpoch, PPolicy: p.PPolicy, BScope: p.BScope,
		OwnedOrigin: p.OwnedOrigin, PendingBytes: p.PendingBytes, PendingCount: p.PendingCount, OutboxBytes: p.OutboxBytes,
	}, Identity: node.Identity, Limits: node.Limits}
	key, err := s3aReadProvisioned(node.Membership.KeyFile, ed25519.PublicKeySize)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errS3AConfig
	}
	c.Membership = peerauth.ControlStoreOptions{Path: node.Membership.Path, Key: ed25519.PublicKey(key), Profile: node.Membership.Profile, Self: node.Membership.Self}
	c.Manifest, err = s3aReadProvisioned(node.Membership.ManifestFile, 256<<10)
	if err != nil {
		return nil, errS3AConfig
	}
	manifest, err := peerauth.VerifyControlManifest(c.Manifest, c.Membership.Key, c.Membership.Profile.Lineage)
	if err != nil || !c.Membership.Profile.Valid() || manifest.Profile.Digest() != c.Membership.Profile.Digest() || c.Membership.Profile.ProtocolScope != trust.scope ||
		!c.Limits.valid(c.Participant) || p.BScope.validateGenesis(trust.genesis) != nil || s3aPaths(&c) != nil {
		return nil, errS3AConfig
	}
	self, found := trust.member(p.Member)
	origin, owned := trust.origin(p.OwnedOrigin)
	if !found || p.Incarnation == [16]byte{} {
		return nil, errS3AConfig
	}
	if p.OwnedOrigin == 0 {
		if owned || node.OriginKeyFile != "" {
			return nil, errS3AConfig
		}
	} else if !owned || !self.Proposer || origin.Member != self.ID || origin.Incarnation != p.Incarnation || node.OriginKeyFile == "" {
		return nil, errS3AConfig
	}
	matched := false
	for _, voter := range c.Membership.Profile.Voters {
		m, exists := trust.member(voter.Voter)
		if !exists || m.PublicKey != voter.Key || m.Proposer != voter.Proposer {
			return nil, errS3AConfig
		}
		matched = matched || voter.Voter == self.ID && voter.Workload == c.Membership.Self
	}
	u, err := url.Parse(c.Membership.Self.Origin)
	_, port, listenErr := net.SplitHostPort(node.ListenAddress)
	n, numberErr := strconv.Atoi(port)
	if !matched || err != nil || listenErr != nil || numberErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || port != u.Port() {
		return nil, errS3AConfig
	}
	profile := CurrentProfile{CurrentPublicVersion, genesis.Domain, genesis.Cohort, genesis.Generation, trust.scope, genesis.TimeProfile, c.Membership.Profile.Digest(), state.configuration.Digest()}
	return &CurrentProvisioning{c, node.OriginKeyFile, node.FloorsFile, node.ListenAddress, profile}, nil
}

func (p *CurrentProvisioning) Profile() CurrentProfile {
	if p == nil {
		return CurrentProfile{}
	}
	return p.profile
}

// OpenCurrentAuthority uses only the production native constructor. Its private
// listener starts before public readiness; quorum absence never falls back to
// OFF or a fixed writer. The owner joins listener/workers/producers on Close.
func OpenCurrentAuthority(ctx context.Context, p *CurrentProvisioning, mode, browserOrigin string) (*CurrentAuthority, error) {
	if p == nil || p.config.Participant.Trust == nil || p.profile.Version != CurrentPublicVersion || mode != "fresh" && mode != "resume" {
		return nil, errS3AConfig
	}
	floors := s3aFloors{}
	if mode == "resume" {
		if _, err := currentDocument(p.floors, 16<<10, &floors); err != nil || floors.M.Binding != p.profile.Membership || floors.M.Version == 0 ||
			floors.P.Scope == [32]byte{} || floors.P.Index == 0 || floors.B.ScopeDigest == [32]byte{} || floors.B.LocalIndex == 0 {
			return nil, errS3AConfig
		}
	} else if p.floors != "" {
		return nil, errS3AConfig
	}
	o, err := openCurrentAuthorityOwner(ctx, p.config, p.originKey, browserOrigin, mode == "fresh", floors)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", p.listen)
	if err != nil {
		return nil, errors.Join(err, o.network.Close())
	}
	if err = o.network.Start(listener); err != nil {
		return nil, errors.Join(err, listener.Close(), o.network.Close())
	}
	return &CurrentAuthority{origin: o}, nil
}

func (o *CurrentAuthority) Close() error {
	if o == nil || o.origin == nil {
		return nil
	}
	err := o.origin.network.Close()
	o.observations.mu.Lock()
	clear(o.observations.items)
	o.observations.order = nil
	o.observations.mu.Unlock()
	return err
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
		return false
	}
	n := o.origin.network
	defer n.calls.Done()
	n.kernel.gate.Lock()
	defer n.kernel.gate.Unlock()
	defer n.kernel.poisonPanic()
	if ctx.Err() != nil || n.kernel.readyLocked() != nil || n.kernel.chosen != nil {
		return false
	}
	start, expiry, err := o.origin.eligibilityLocked(ctx)
	if err != nil {
		return false
	}
	now, _, err := n.receiver.currentLocked()
	return err == nil && !time.Unix(0, int64(now.utc.low)).Before(start) && time.Unix(0, int64(now.utc.high)).Before(expiry)
}

// ExportFloors returns only independent minimum-cut data, never a resume
// capability. Operators retain it beside the provisioned genesis and identity.
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
