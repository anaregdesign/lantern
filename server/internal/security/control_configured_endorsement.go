package security

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

const configuredEndorsementPath = "/lantern-private/control/v1/configured-trust-endorsement"
const configuredEndorsementLimit = 16 << 10

// Endorsements approve one exact public intent at one native predecessor.
// They are nonexclusive: concurrent majority-approved intents are resolved by
// the existing Paxos slot, which preserves its highest accepted value on retry.
// No configuration can enter that slot on only a minority's startup intent.
type configuredTrustStatement struct {
	Version     uint16
	Scope       [32]byte
	Reviewed    SemanticCut
	Predecessor [32]byte
	Desired     [32]byte
	Slot        uint64
	TimeProfile [32]byte
}

type configuredTrustEndorsement struct {
	Member               uint32
	Signature            []byte
	NotBefore, ExpiresAt time.Time
}

func configuredStatement(h configuredTrustHeader) configuredTrustStatement {
	return configuredTrustStatement{h.Version, h.Scope, h.Reviewed, h.Predecessor, h.Desired, h.Slot, sha256.Sum256([]byte(authorityTimeProfileDescription))}
}

func configuredEndorsementVoteBody(s configuredTrustStatement, v configuredTrustEndorsement) []byte {
	raw, _ := json.Marshal(struct {
		Statement            configuredTrustStatement
		Member               uint32
		NotBefore, ExpiresAt time.Time
	}{s, v.Member, v.NotBefore, v.ExpiresAt})
	return append([]byte("lantern/security/configured-trust/endorsement-vote\x00\x01"), raw...)
}

func verifyConfiguredEndorsementWindow(votes []configuredTrustEndorsement, low, high time.Time) bool {
	if !s2cCanonicalTime(low, false) || !s2cCanonicalTime(high, false) || high.Before(low) {
		return false
	}
	for _, vote := range votes {
		if low.Before(vote.NotBefore) || !high.Before(vote.ExpiresAt) {
			return false
		}
	}
	return true
}

func verifyConfiguredEndorsements(t *s2cTrust, s configuredTrustStatement, votes []configuredTrustEndorsement) bool {
	if t == nil || s.Version != configuredTrustVersion || s.TimeProfile != sha256.Sum256([]byte(authorityTimeProfileDescription)) || s.Scope != t.scope || !s2SameScope(s.Reviewed, t.genesis.state.projection.cut) || s.Predecessor == [32]byte{} || s.Desired == [32]byte{} || len(votes) < t.majority() || len(votes) > len(t.members) {
		return false
	}
	seen := map[uint32]bool{}
	for i, vote := range votes {
		d, known := t.member(vote.Member)
		if !known || configuredVoterCapability(t, d) == [32]byte{} || seen[d.ID] || i > 0 && votes[i-1].Member >= vote.Member || len(vote.Signature) != ed25519.SignatureSize || !s2cCanonicalTime(vote.NotBefore, false) || !s2cCanonicalTime(vote.ExpiresAt, false) || !vote.ExpiresAt.After(vote.NotBefore) || vote.ExpiresAt.Sub(vote.NotBefore) > time.Duration(authorityRenewalLifetime) || !ed25519.Verify(ed25519.PublicKey(d.PublicKey[:]), configuredEndorsementVoteBody(s, vote), vote.Signature) {
			return false
		}
		seen[d.ID] = true
	}
	return len(seen) >= t.majority()
}

func (o *authorityOriginOwner) endorseConfiguredLocked(s configuredTrustStatement) (configuredTrustEndorsement, error) {
	k := o.network.kernel
	if o.closed || k.readyLocked() != nil || k.chosen != nil || !k.config.configured.valid() || s.Version != configuredTrustVersion || s.TimeProfile != sha256.Sum256([]byte(authorityTimeProfileDescription)) || s.Scope != k.trust.scope || s.Desired != k.config.configured.Digest() || s.Reviewed != k.replayState.projection.cut || s.Predecessor != k.replayState.prefix || s.Slot != k.replayState.slot || configuredVoterCapabilityFor(k.trust, k.config.Member) == [32]byte{} || len(k.key) != ed25519.PrivateKeySize {
		return configuredTrustEndorsement{}, ErrPermissionDenied
	}
	// Refuse invalid state/grant/last-admin transitions before endorsing. This is
	// pure and cannot turn an endorsement into materialization or public access.
	if _, err := assessConfiguredTrust(k.replayState.projection, k.config.configured); err != nil {
		return configuredTrustEndorsement{}, err
	}
	start, expiry, err := o.eligibilityLocked(o.network.ctx)
	if err != nil {
		return configuredTrustEndorsement{}, err
	}
	now, err := o.network.timeOwner.current()
	if err != nil {
		return configuredTrustEndorsement{}, err
	}
	low, high := time.Unix(0, int64(now.utc.low)).UTC(), time.Unix(0, int64(now.utc.high)).UTC()
	if low.Before(start) || !high.Before(expiry) {
		return configuredTrustEndorsement{}, ErrAuthorityUnavailable
	}
	expires := low.Add(time.Duration(authorityRenewalLifetime))
	if expiry.Before(expires) {
		expires = expiry
	}
	if !high.Before(expires) {
		return configuredTrustEndorsement{}, ErrAuthorityUnavailable
	}
	vote := configuredTrustEndorsement{Member: k.config.Member, NotBefore: low, ExpiresAt: expires}
	vote.Signature = ed25519.Sign(k.key, configuredEndorsementVoteBody(s, vote))
	return vote, nil
}

func (o *s3aOwner) configuredEndorsementResponse(ctx context.Context, raw []byte, check func() error) ([]byte, error) {
	var s configuredTrustStatement
	if len(raw) > configuredEndorsementLimit || s2StrictJSON(raw, &s, DefaultPolicyLimits()) != nil || o.origin == nil {
		return nil, ErrPermissionDenied
	}
	o.kernel.gate.Lock()
	defer o.kernel.gate.Unlock()
	defer o.kernel.poisonPanic()
	if ctx.Err() != nil || check() != nil {
		return nil, ErrAuthorityUnavailable
	}
	vote, err := o.origin.endorseConfiguredLocked(s)
	if err != nil {
		return nil, err
	}
	return json.Marshal(vote)
}

func (o *authorityOriginOwner) collectConfiguredEndorsements(ctx context.Context, s configuredTrustStatement) ([]configuredTrustEndorsement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := o.network
	k := n.kernel
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var votes []configuredTrustEndorsement
	k.gate.Lock()
	own, err := o.endorseConfiguredLocked(s)
	k.gate.Unlock()
	if err != nil {
		return nil, err
	}
	votes = append(votes, own)
	ids := make([]uint32, 0, len(n.peers))
	for id := range n.peers {
		if id != k.config.Member {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		response, status, admission, requestErr := n.requestAdmitted(ctx, id, configuredEndorsementPath, raw, configuredEndorsementLimit)
		if requestErr != nil || status != http.StatusOK || admission.Check(ctx) != nil {
			continue
		}
		var vote configuredTrustEndorsement
		if s2StrictJSON(response, &vote, DefaultPolicyLimits()) != nil {
			continue
		}
		d, known := k.trust.member(vote.Member)
		if !known || d.ID != id || configuredVoterCapability(k.trust, d) == [32]byte{} || len(vote.Signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(d.PublicKey[:]), configuredEndorsementVoteBody(s, vote), vote.Signature) {
			continue
		}
		votes = append(votes, vote)
		if len(votes) >= k.trust.majority() {
			break
		}
	}
	sort.Slice(votes, func(i, j int) bool { return votes[i].Member < votes[j].Member })
	if !verifyConfiguredEndorsements(k.trust, s, votes) {
		return nil, ErrAuthorityUnavailable
	}
	return votes, nil
}

func configuredVoterCapabilityFor(t *s2cTrust, id uint32) [32]byte {
	member, known := t.member(id)
	if !known {
		return [32]byte{}
	}
	return configuredVoterCapability(t, member)
}

func configuredVoterCapability(t *s2cTrust, member s2cMember) [32]byte {
	enrolled, known := t.member(member.ID)
	if !known || enrolled != member || t.scope == [32]byte{} || member.ConfiguredTrust != configuredTrustVersion || len(t.origins) == 0 || t.origins[0].Profile != authorityAdmissionProfile() {
		return [32]byte{}
	}
	return s2cHash("configured-trust-voter-capability-v1", struct {
		Scope, Members [32]byte
		Member         s2cMember
	}{t.scope, t.memberSet, member})
}
